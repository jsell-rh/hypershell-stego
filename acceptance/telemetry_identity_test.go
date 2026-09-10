package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"regexp"
	"testing"
	"time"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

var telemetryInstancePattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func telemetryInstance(t testing.TB, attrs []*commonpb.KeyValue, service string) string {
	t.Helper()
	id := signalAttribute(attrs, "service.instance.id").GetStringValue()
	if len(attrs) != 2 || signalAttribute(attrs, "service.name").GetStringValue() != service || !telemetryInstancePattern.MatchString(id) {
		t.Fatal("invalid telemetry instance identity", attrs)
	}
	return id
}

func TestGatewayTelemetrySeparatesReplicasAndRestart(t *testing.T) {
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
	consumer := kafkaConsumer(t, brokerConfig)
	key, settings := issuer(t)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), "OTEL_EXPORTER_OTLP_ENDPOINT=https://"+listener.Addr().String(), "OTEL_EXPORTER_OTLP_CERTIFICATE="+cert.config.CAFile, "OTEL_SERVICE_NAME=hypershell-replicas", "OTEL_TRACES_SAMPLER_ARG=1", "OTEL_METRIC_EXPORT_INTERVAL=1000")
	binary := buildApplication(t)
	stopA, addressA, _, _, outputA := startBothWithLogs(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stopA() }()
	stopB, addressB, grpcB, _, outputB := startBothWithLogs(t, binary, f.dsn, brokerConfig, settings...)
	defer stopB()
	creator := token(t, key, "alice", "gateway:creator")
	owner := token(t, key, "alice")
	input, _ := json.Marshal(f.request("private-replica-gateway"))
	code, body := requestJSON(t, "POST", addressA+"/api/hypershell/v1/gateways", creator, input)
	var created struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(body, &created) != nil || created.ID == "" {
		t.Fatal("replica A did not create Gateway", code)
	}
	var grants int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE gateway_id=$1 AND deleted_at IS NULL", created.ID).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("owner grant was not committed with Gateway", err, grants)
	}
	client, connection := grpcClient(t, grpcB, cert)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	got, err := client.GetGateway(metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+owner)), &pb.GetGatewayRequest{Id: created.ID})
	cancel()
	connection.Close()
	if err != nil || got.GetGateway().GetName() != "private-replica-gateway" {
		t.Fatal("replica B did not read Gateway", err)
	}
	response, err := (&http.Client{Timeout: 2 * time.Second}).Get(addressB + "/api/hypershell/v1/gateways/" + created.ID)
	if err != nil {
		t.Fatal(err)
	}
	io.Copy(io.Discard, response.Body)
	response.Body.Close()
	if response.StatusCode != 401 {
		t.Fatal("replica B accepted missing credentials")
	}
	if readEvent(t, consumer, created.ID) == "" {
		t.Fatal("Gateway event was not delivered")
	}
	stopA()
	firstOutput := outputA()
	stopA, addressA, _, _, outputA = startBothWithLogs(t, binary, f.dsn, brokerConfig, settings...)
	code, body = requestJSON(t, "GET", addressA+"/api/hypershell/v1/gateways/"+created.ID, owner, nil)
	if code != 200 || !bytes.Contains(body, []byte("private-replica-gateway")) {
		t.Fatal("restarted replica did not retain Gateway", code)
	}
	stopA()
	stopB()
	private := []string{creator, owner, created.ID, "private-replica-gateway"}
	// Request counts differ by transport and runtime. Preserve each cumulative
	// stream separately, including any periodic samples before the final flush.
	observed := map[string]map[string]*metricpb.Metric{}
	spanInstances, logInstances := map[string]string{}, map[string]string{}
	exemplarInstances := map[string]string{}
	starts, stops := map[string]int{}, map[string]int{}
	readResource := func(attrs []*commonpb.KeyValue) string { return telemetryInstance(t, attrs, "hypershell-replicas") }
	safe := func(message proto.Message) {
		t.Helper()
		encoded, _ := proto.Marshal(message)
		for _, value := range private {
			if bytes.Contains(encoded, []byte(value)) {
				t.Fatal("replica telemetry exposed private data")
			}
		}
	}
	count := func(instance, name string) uint64 {
		var total uint64
		for _, point := range observed[instance][name].GetHistogram().GetDataPoints() {
			total += point.Count
		}
		return total
	}
	complete := func() bool {
		if len(starts) != 3 || len(stops) != 3 || len(spanInstances) != 4 || len(logInstances) != 4 || len(exemplarInstances) != 4 || len(observed) != 3 {
			return false
		}
		var httpCount, rpcCount uint64
		for instance := range observed {
			httpCount += count(instance, "http.server.request.duration")
			rpcCount += count(instance, "rpc.server.call.duration")
		}
		return httpCount == 3 && rpcCount == 1
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for !complete() {
		select {
		case batch := <-traces.received:
			safe(batch)
			for _, resource := range batch.ResourceSpans {
				instance := readResource(resource.Resource.Attributes)
				for _, scope := range resource.ScopeSpans {
					for _, span := range scope.Spans {
						spanInstances[hex.EncodeToString(span.SpanId)] = instance
					}
				}
			}
		case batch := <-logs.received:
			safe(batch)
			for _, resource := range batch.ResourceLogs {
				instance := readResource(resource.Resource.Attributes)
				for _, scope := range resource.ScopeLogs {
					for _, record := range scope.LogRecords {
						switch record.EventName {
						case "telemetry.runtime.started":
							starts[instance]++
						case "telemetry.runtime.stopped":
							stops[instance]++
						case "http.server.request.completed", "rpc.server.call.completed":
							logInstances[hex.EncodeToString(record.SpanId)] = instance
						default:
							t.Fatal("unexpected replica log event", record.EventName)
						}
					}
				}
			}
		case batch := <-metrics.received:
			safe(batch)
			for _, resource := range batch.ResourceMetrics {
				instance := readResource(resource.Resource.Attributes)
				if observed[instance] == nil {
					observed[instance] = map[string]*metricpb.Metric{}
				}
				for _, scope := range resource.ScopeMetrics {
					for _, metric := range scope.Metrics {
						observed[instance][metric.Name] = metric
						for _, point := range metric.GetHistogram().GetDataPoints() {
							for _, exemplar := range point.Exemplars {
								exemplarInstances[hex.EncodeToString(exemplar.SpanId)] = instance
							}
						}
					}
				}
			}
		case <-timer.C:
			t.Fatal("replica signals did not retain separate streams")
		}
	}
	first := checkRuntimeLogs(t, firstOutput, "hypershell-replicas", private...)
	second := checkRuntimeLogs(t, outputB(), "hypershell-replicas", private...)
	restarted := checkRuntimeLogs(t, outputA(), "hypershell-replicas", private...)
	if first == second || first == restarted || second == restarted {
		t.Fatal("replicas or restart reused an instance ID")
	}
	for _, instance := range []string{first, second, restarted} {
		if starts[instance] != 1 || stops[instance] != 1 || count(instance, "http.server.request.duration") != 1 {
			t.Fatal("runtime count is missing or merged", instance)
		}
		wantRPC := uint64(0)
		if instance == second {
			wantRPC = 1
		}
		if count(instance, "rpc.server.call.duration") != wantRPC {
			t.Fatal("RPC count belongs to another runtime")
		}
	}
	for span, instance := range spanInstances {
		if logInstances[span] != instance || exemplarInstances[span] != instance {
			t.Fatal("log and span instance identities differ")
		}
	}
	for instance, instruments := range observed {
		for _, instrument := range instruments {
			for _, point := range instrument.GetHistogram().GetDataPoints() {
				if signalAttribute(point.Attributes, "service.instance.id") != nil {
					t.Fatal("instance identity became a metric label")
				}
				for _, exemplar := range point.Exemplars {
					if spanInstances[hex.EncodeToString(exemplar.SpanId)] != instance {
						t.Fatal("metric exemplar belongs to another runtime")
					}
				}
			}
		}
	}
}
