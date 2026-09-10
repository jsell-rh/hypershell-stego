package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"net"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
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

func TestGatewayControllerTelemetryAcrossFailureAndRestart(t *testing.T) {
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
	settings = append(settings, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), "OTEL_EXPORTER_OTLP_ENDPOINT=", "OTEL_SERVICE_NAME=hypershell-api")
	settings = withControllerWriteGrants(t, settings, writeGrant("controller", "configure.identity", ""))
	binary := buildApplication(t)
	stopAPI, httpAddress, address := startBoth(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stopAPI() }()
	public, connection := grpcClient(t, address, cert)
	defer connection.Close()
	states := control.NewGatewayIdentityServiceClient(connection)
	controllerToken := token(t, key, "controller")
	owner := token(t, key, "alice", "gateway:creator")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	auth := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+controllerToken))
	provider := &failedIdentityProvider{checkpointProvider: &checkpointProvider{roles: make(map[string][]string)}}
	controller, err := gatewayidentity.New(public, states, provider)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://"+listener.Addr().String())
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", cert.config.CAFile)
	t.Setenv("OTEL_SERVICE_NAME", "gateway-identity")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "1000")
	work, cancelWork := context.WithCancel(auth)
	done := make(chan error, 1)
	go func() { done <- controller.Run(work) }()
	var once sync.Once
	stopController := func() {
		once.Do(func() {
			cancelWork()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(6 * time.Second):
				t.Error("controller shutdown exceeded its bound")
			}
		})
	}
	defer stopController()
	spans := map[string]*tracepb.Span{}
	records := map[string]*logpb.LogRecord{}
	measured := map[string]*metricpb.Metric{}
	events := map[string]int{}
	controllerInstance := ""
	checkInstance := func(attrs []*commonpb.KeyValue) {
		t.Helper()
		id := telemetryInstance(t, attrs, "gateway-identity")
		if controllerInstance != "" && controllerInstance != id {
			t.Fatal("API restart changed controller instance identity")
		}
		controllerInstance = id
	}
	private := []string{controllerToken, owner, "private-provider-gateway", "private provider credential=do-not-publish"}
	receive := func(deadline <-chan time.Time) {
		t.Helper()
		check := func(message proto.Message) {
			t.Helper()
			data, _ := proto.Marshal(message)
			for _, secret := range private {
				if bytes.Contains(data, []byte(secret)) {
					t.Fatal("controller telemetry exposed private data")
				}
			}
		}
		select {
		case batch := <-traces.received:
			check(batch)
			for _, resource := range batch.ResourceSpans {
				checkInstance(resource.Resource.Attributes)
				for _, scope := range resource.ScopeSpans {
					for _, span := range scope.Spans {
						spans[hex.EncodeToString(span.SpanId)] = span
					}
				}
			}
		case batch := <-logs.received:
			check(batch)
			for _, resource := range batch.ResourceLogs {
				checkInstance(resource.Resource.Attributes)
				for _, scope := range resource.ScopeLogs {
					for _, record := range scope.LogRecords {
						events[record.EventName]++
						if record.EventName == "controller.work.completed" {
							records[hex.EncodeToString(record.SpanId)] = record
						}
					}
				}
			}
		case batch := <-metrics.received:
			check(batch)
			for _, resource := range batch.ResourceMetrics {
				checkInstance(resource.Resource.Attributes)
				for _, scope := range resource.ScopeMetrics {
					for _, metric := range scope.Metrics {
						measured[metric.Name] = metric
					}
				}
			}
		case <-deadline:
			t.Fatal("controller telemetry did not arrive")
		}
	}
	await := func(check func() bool) {
		t.Helper()
		timer := time.NewTimer(6 * time.Second)
		defer timer.Stop()
		for !check() {
			receive(timer.C)
		}
	}
	// A sequence retains its increment after transaction rollback. Abort the
	// first insert so the documented serialization retry path is always tested.
	if _, err := f.db.Exec(`CREATE SEQUENCE telemetry_create_attempt;
CREATE FUNCTION abort_first_telemetry_create() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF nextval('telemetry_create_attempt') = 1 THEN
  RAISE EXCEPTION 'test serialization abort' USING ERRCODE='40001';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER abort_first_telemetry_create BEFORE INSERT ON gateways FOR EACH ROW EXECUTE FUNCTION abort_first_telemetry_create()`); err != nil {
		t.Fatal(err)
	}
	requestBody, _ := json.Marshal(f.request("private-provider-gateway"))
	var code int
	var body []byte
	retries := 0
	for attempt := 0; attempt < 5; attempt++ {
		code, body = requestJSON(t, "POST", httpAddress+"/api/hypershell/v1/gateways", owner, requestBody)
		if code != 409 {
			break
		}
		var problem struct {
			Reason string `json:"reason"`
		}
		if json.Unmarshal(body, &problem) != nil || problem.Reason != "The resource changed during the request; retry the operation" {
			t.Fatal("unexpected Gateway conflict", string(body))
		}
		var gateways, grants int
		if err := f.db.QueryRow("SELECT (SELECT count(*) FROM gateways), (SELECT count(*) FROM role_bindings WHERE scope='gateway')").Scan(&gateways, &grants); err != nil || gateways != 0 || grants != 0 {
			t.Fatal("serialization abort retained a Gateway or owner grant", err)
		}
		retries++
		time.Sleep(50 * time.Millisecond)
	}
	if retries == 0 {
		t.Fatal("serialization retry was not tested")
	}

	var created struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(body, &created) != nil || created.ID == "" {
		t.Fatal("Gateway create failed", code, string(body))
	}
	if _, err := f.db.Exec(`DROP TRIGGER abort_first_telemetry_create ON gateways; DROP FUNCTION abort_first_telemetry_create(); DROP SEQUENCE telemetry_create_attempt`); err != nil {
		t.Fatal(err)
	}
	private = append(private, created.ID)
	waitCondition := func(reason string) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			state, err := states.GetGatewayIdentityState(auth, &control.GetGatewayIdentityStateRequest{Id: created.ID}, grpc.WaitForReady(true))
			if err == nil && state.GetConditions()["identity"].GetConditions()["ClientReady"].GetReason() == reason && state.GetConditions()["identity"].GetConditions()["ClientReady"].GetCurrent() {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("controller condition did not change", reason, err)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	correlated := func(outcome string) bool {
		for id, record := range records {
			span := spans[id]
			if span != nil && signalAttribute(record.Attributes, "operation").GetStringValue() == "reconcile" && signalAttribute(record.Attributes, "outcome").GetStringValue() == outcome {
				if !bytes.Equal(span.TraceId, record.TraceId) || len(record.Attributes) != 4 || len(span.Attributes) != 3 {
					t.Fatal("work log and span differ")
				}
				return true
			}
		}
		return false
	}
	waitCondition("IdentityProviderUnavailable")
	await(func() bool {
		return events["controller.watch.started"] > 0 && correlated("failure") && len(measured["stego.controller.retries"].GetSum().GetDataPoints()) > 0
	})
	if points := measured["stego.controller.queue.running"].GetGauge().GetDataPoints(); len(points) != 1 || points[0].GetAsInt() != 1 {
		t.Fatal("controller queue was not measured")
	}
	provider.ready.Store(true)
	waitCondition("IdentityClientReady")
	await(func() bool { return correlated("success") })
	stopAPI()
	restart := append(append([]string{}, settings...), "STEGO_GRPC_ADDR="+address)
	stopAPI, httpAddress, _ = startBoth(t, binary, f.dsn, brokerConfig, restart...)
	await(func() bool {
		return events["controller.watch.reconnect"] > 0 && events["controller.watch.started"] >= 2
	})
	waitCondition("IdentityClientReady")
	collector.Stop()
	// A later desired-state change must reconcile while export fails.
	code, body = requestJSON(t, "PATCH", httpAddress+"/api/hypershell/v1/gateways/"+created.ID, owner, []byte(`{"name":"private-after-collector-loss"}`))
	if code != 200 {
		t.Fatal("Gateway update failed after collector loss", code)
	}
	waitCondition("IdentityClientReady")
	code, _ = requestJSON(t, "GET", httpAddress+"/api/hypershell/v1/gateways/"+created.ID, owner, nil)
	if code != 200 {
		t.Fatal("Gateway read failed after collector loss", code)
	}
	stopController()
}
