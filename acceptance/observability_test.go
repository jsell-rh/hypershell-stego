package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type workflowLogCollector struct {
	logcollector.UnimplementedLogsServiceServer
	received chan *logcollector.ExportLogsServiceRequest
}

func (c *workflowLogCollector) Export(ctx context.Context, request *logcollector.ExportLogsServiceRequest) (*logcollector.ExportLogsServiceResponse, error) {
	select {
	case c.received <- request:
		return &logcollector.ExportLogsServiceResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type workflowMetricCollector struct {
	metriccollector.UnimplementedMetricsServiceServer
	received chan *metriccollector.ExportMetricsServiceRequest
}

func (c *workflowMetricCollector) Export(ctx context.Context, request *metriccollector.ExportMetricsServiceRequest) (*metriccollector.ExportMetricsServiceResponse, error) {
	select {
	case c.received <- request:
		return &metriccollector.ExportMetricsServiceResponse{}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
func signalAttribute(attrs []*commonpb.KeyValue, key string) *commonpb.AnyValue {
	for _, attr := range attrs {
		if attr.Key == key {
			return attr.Value
		}
	}
	return nil
}

func TestGatewayLogsMetricsAndTracesAcrossRestart(t *testing.T) {
	f := database(t)
	cert := identity(t, "localhost")
	directory := filepath.Dir(cert.config.CAFile)
	pair, err := tls.LoadX509KeyPair(filepath.Join(directory, "server.pem"), filepath.Join(directory, "server-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	collector := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}})))
	traces := &workflowTraceCollector{received: make(chan *tracecollector.ExportTraceServiceRequest, 64)}
	logs := &workflowLogCollector{received: make(chan *logcollector.ExportLogsServiceRequest, 64)}
	metrics := &workflowMetricCollector{received: make(chan *metriccollector.ExportMetricsServiceRequest, 64)}
	tracecollector.RegisterTraceServiceServer(collector, traces)
	logcollector.RegisterLogsServiceServer(collector, logs)
	metriccollector.RegisterMetricsServiceServer(collector, metrics)
	go collector.Serve(listener)
	defer collector.Stop()
	_, brokerConfig := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), "OTEL_EXPORTER_OTLP_ENDPOINT=https://"+listener.Addr().String(), "OTEL_EXPORTER_OTLP_CERTIFICATE="+cert.config.CAFile, "OTEL_SERVICE_NAME=hypershell-api-server", "OTEL_TRACES_SAMPLER_ARG=1")
	binary := buildApplication(t)
	stop, httpAddress, grpcAddress := startBoth(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stop() }()
	client, connection := grpcClient(t, grpcAddress, cert)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	owner := token(t, key, "alice")
	creator := token(t, key, "alice", "gateway:creator")
	call := func(id, credential string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("traceparent", "00-"+id+"-2222222222222222-01", "authorization", "Bearer "+credential, "baggage", "user=private-signal-user", "tracestate", "vendor=private-signal-state"))
	}
	private := []string{owner, creator, "private-signal-user", "private-signal-state", "private-signal-gateway"}
	spans := map[string]*tracepb.Span{}
	records := map[string]*logpb.LogRecord{}
	observedMetrics := map[string]*metricpb.Metric{}
	checkPrivate := func(message proto.Message) {
		t.Helper()
		data, _ := proto.Marshal(message)
		for _, secret := range private {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("telemetry exposed private Gateway data")
			}
		}
	}
	receive := func(deadline <-chan time.Time) {
		t.Helper()
		check := func(attrs []*commonpb.KeyValue) {
			if len(attrs) != 1 || signalAttribute(attrs, "service.name").GetStringValue() != "hypershell-api-server" {
				t.Fatal("telemetry lost service identity")
			}
		}
		select {
		case request := <-traces.received:
			checkPrivate(request)
			for _, resource := range request.ResourceSpans {
				check(resource.Resource.Attributes)
				for _, scope := range resource.ScopeSpans {
					for _, span := range scope.Spans {
						spans[hex.EncodeToString(span.TraceId)] = span
					}
				}
			}
		case request := <-logs.received:
			checkPrivate(request)
			for _, resource := range request.ResourceLogs {
				check(resource.Resource.Attributes)
				for _, scope := range resource.ScopeLogs {
					for _, record := range scope.LogRecords {
						records[hex.EncodeToString(record.TraceId)] = record
					}
				}
			}
		case request := <-metrics.received:
			checkPrivate(request)
			for _, resource := range request.ResourceMetrics {
				check(resource.Resource.Attributes)
				for _, scope := range resource.ScopeMetrics {
					for _, metric := range scope.Metrics {
						observedMetrics[metric.Name] = metric
					}
				}
			}
		case <-deadline:
			t.Fatal("Gateway telemetry did not arrive")
		}
	}
	awaitCorrelation := func(id string) {
		t.Helper()
		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		for spans[id] == nil || records[id] == nil {
			receive(timer.C)
		}
		span, record := spans[id], records[id]
		if hex.EncodeToString(span.ParentSpanId) != "2222222222222222" || !bytes.Equal(span.SpanId, record.SpanId) || record.TimeUnixNano < span.StartTimeUnixNano || record.EventName == "" || record.SeverityNumber == 0 || len(record.Attributes) != len(span.Attributes)+1 {
			t.Fatal("request log did not correlate with its span")
		}
		for _, attribute := range span.Attributes {
			if !proto.Equal(attribute.Value, signalAttribute(record.Attributes, attribute.Key)) {
				t.Fatal("log and span fields differ")
			}
		}
	}
	httpCall := func(id, credential, gateway string, want int) {
		t.Helper()
		request, err := http.NewRequest("GET", httpAddress+"/api/hypershell/v1/gateways/"+gateway, nil)
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("traceparent", "00-"+id+"-2222222222222222-01")
		request.Header.Set("baggage", "user=private-signal-user")
		if credential != "" {
			request.Header.Set("authorization", "Bearer "+credential)
		}
		response, err := (&http.Client{Timeout: 2 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		if response.StatusCode != want {
			t.Fatal("telemetry changed HTTP behavior", response.StatusCode, want)
		}
	}
	const watchID = "11111111111111111111111111111111"
	watch := watchGateways(t, client, call(watchID, owner))
	const createID = "33333333333333333333333333333333"
	created, err := client.CreateGateway(call(createID, creator), &pb.CreateGatewayRequest{Name: "private-signal-gateway", ClusterId: f.cluster, ReleaseId: f.release})
	if err != nil {
		t.Fatal(err)
	}
	id := created.GetGateway().GetMetadata().GetId()
	if id == "" {
		t.Fatal("missing Gateway ID")
	}
	private = append(private, id)
	watch.expect(t, pb.EventType_EVENT_TYPE_CREATED, id, "private-signal-gateway")
	awaitCorrelation(createID)
	const readID = "44444444444444444444444444444444"
	httpCall(readID, owner, id, 200)
	awaitCorrelation(readID)
	const deniedID = "55555555555555555555555555555555"
	httpCall(deniedID, "", id, 401)
	awaitCorrelation(deniedID)
	timer := time.NewTimer(15 * time.Second)
	for {
		metric := observedMetrics["rpc.server.active_requests"]
		if metric != nil && len(metric.GetSum().DataPoints) == 1 && metric.GetSum().DataPoints[0].GetAsInt() == 1 {
			break
		}
		receive(timer.C)
	}
	timer.Stop()
	if spans[watchID] != nil || records[watchID] != nil {
		t.Fatal("watch telemetry completed before the stream ended")
	}
	watch.cancel()
	awaitCorrelation(watchID)
	connection.Close()
	stop()
	timer = time.NewTimer(5 * time.Second)
	for {
		metric := observedMetrics["rpc.server.active_requests"]
		if metric != nil && len(metric.GetSum().DataPoints) == 1 && metric.GetSum().DataPoints[0].GetAsInt() == 0 {
			break
		}
		receive(timer.C)
	}
	timer.Stop()
	for _, name := range []string{"http.server.request.duration", "rpc.server.call.duration"} {
		metric := observedMetrics[name]
		if metric == nil || metric.Unit != "s" {
			t.Fatal("duration metric missing", name)
		}
		var count uint64
		for _, point := range metric.GetHistogram().DataPoints {
			count += point.Count
		}
		if count != 2 {
			t.Fatal("duration metric lost calls", name, count)
		}
	}
	observedMetrics = map[string]*metricpb.Metric{}
	stop, httpAddress, grpcAddress = startBoth(t, binary, f.dsn, brokerConfig, settings...)
	client, connection = grpcClient(t, grpcAddress, cert)
	defer connection.Close()
	const restartID = "66666666666666666666666666666666"
	got, err := client.GetGateway(call(restartID, owner), &pb.GetGatewayRequest{Id: id})
	if err != nil || got.GetGateway().GetName() != "private-signal-gateway" {
		t.Fatal("Gateway restart failed", err)
	}
	awaitCorrelation(restartID)
	timer = time.NewTimer(15 * time.Second)
	for {
		found := false
		if metric := observedMetrics["rpc.server.call.duration"]; metric != nil {
			for _, point := range metric.GetHistogram().DataPoints {
				for _, exemplar := range point.Exemplars {
					if hex.EncodeToString(exemplar.TraceId) == restartID && point.Count == 1 {
						found = true
					}
				}
			}
		}
		if found {
			break
		}
		receive(timer.C)
	}
	timer.Stop()
	collector.Stop()
	httpCall("77777777777777777777777777777777", owner, id, 200)
	started := time.Now()
	stop()
	if time.Since(started) > 8*time.Second {
		t.Fatal("collector failure blocked shutdown")
	}
}
