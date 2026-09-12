package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// The overlay adds a fault only to this test build. Generated files stay intact.
func buildApplicationWithHTTPFault(t *testing.T) string {
	t.Helper()
	return buildApplicationWithMainOverlay(t, func(source string) string {
		anchor := "mux := http.NewServeMux()"
		if strings.Count(source, anchor) != 1 {
			t.Fatal("generated HTTP setup changed")
		}
		return strings.Replace(source, anchor, anchor+`
 mux.HandleFunc("GET /_test/panic", func(w http.ResponseWriter, r *http.Request) { panic(r.Header.Get("X-Test-Fault")) })
 `, 1)
	})
}

func buildApplicationWithMainOverlay(t *testing.T, transform func(string) string) string {
	t.Helper()
	return buildProgramWithMainOverlay(t, "./out", transform)
}

func buildProgramWithMainOverlay(t *testing.T, pkg string, transform func(string) string) string {
	t.Helper()
	original, err := filepath.Abs(filepath.Join("..", pkg, "main.go"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := os.ReadFile(original)
	if err != nil {
		t.Fatal(err)
	}
	source = []byte(transform(string(source)))
	directory := t.TempDir()
	replacement := filepath.Join(directory, "main.go")
	if err := os.WriteFile(replacement, source, 0600); err != nil {
		t.Fatal(err)
	}
	overlay, _ := json.Marshal(map[string]any{"Replace": map[string]string{original: replacement}})
	overlayPath := filepath.Join(directory, "overlay.json")
	if err := os.WriteFile(overlayPath, overlay, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(directory, "hypershell")
	args := []string{"build", "-mod=readonly", "-overlay", overlayPath, "-o", binary}
	if raceEnabled {
		args = append(args, "-race")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", append(args, pkg)...)
	command.Dir = ".."
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("build process fault overlay: %v\n%s", err, output)
	}
	return binary
}

func TestGatewayHTTPDiagnosticPrivacy(t *testing.T) {
	binary := buildApplicationWithHTTPFault(t)
	for _, exported := range []bool{false, true} {
		name := "local"
		if exported {
			name = "OTLP"
		}
		t.Run(name, func(t *testing.T) { gatewayHTTPDiagnosticPrivacy(t, binary, exported) })
	}
}

func gatewayHTTPDiagnosticPrivacy(t *testing.T, binary string, exported bool) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	settings = append(settings, "OTEL_EXPORTER_OTLP_ENDPOINT=", "OTEL_SERVICE_NAME=hypershell-http-diagnostics")
	var signals *httpDiagnosticCollector
	if exported {
		var extra []string
		signals, extra = newHTTPDiagnosticCollector(t)
		settings = append(settings, extra...)
	}
	stop, address, _, _, output := startBothWithLogs(t, binary, f.dsn, config, settings...)
	creator := token(t, key, "alice", "gateway:creator")
	owner := token(t, key, "alice")
	input, _ := json.Marshal(f.request("private-http-diagnostic-gateway"))
	code, data := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", creator, input)
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(data, &gateway) != nil {
		t.Fatal("Gateway creation failed", code)
	}
	if readEvent(t, consumer, gateway.ID) == "" {
		t.Fatal("Gateway event was not delivered")
	}
	awaitQueueEmpty(t, f)
	var grants int
	if err := f.db.QueryRowContext(context.Background(), "SELECT count(*) FROM role_bindings WHERE gateway_id=$1", gateway.ID).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("Gateway owner grant was not committed", err)
	}
	request, _ := http.NewRequest("GET", address+"/_test/panic", nil)
	request.Header.Set("X-Test-Fault", "private-http-panic-value")
	transport := &http.Transport{DisableKeepAlives: true}
	defer transport.CloseIdleConnections()
	response, err := (&http.Client{Transport: transport, Timeout: 5 * time.Second}).Do(request)
	if err == nil {
		response.Body.Close()
		t.Fatal("panic did not abort the HTTP request")
	}
	code, _ = requestJSON(t, "GET", address+"/api/hypershell/v1/gateways/"+gateway.ID, owner, nil)
	if code != 200 {
		t.Fatal("Gateway was not readable after the panic", code)
	}
	stop()
	privateValues := []string{"private-http-panic-value", "private-http-diagnostic-gateway", gateway.ID, creator, "goroutine ", "main.go:"}
	for _, private := range privateValues {
		if strings.Contains(output(), private) {
			t.Fatal("HTTP diagnostic exposed private data", private)
		}
	}
	diagnosticCount := 0
	instance := ""
	for _, line := range strings.Split(output(), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil || record["event.name"] != "http.server.diagnostic" {
			continue
		}
		diagnosticCount++
		instance, _ = record["service.instance.id"].(string)
		if !telemetryInstancePattern.MatchString(instance) {
			t.Fatal("missing diagnostic runtime identity")
		}
		if len(record) != 6 || record["severity"] != "ERROR" || record["service.name"] != "hypershell-http-diagnostics" {
			t.Fatal("invalid HTTP diagnostic record", record)
		}
	}
	if diagnosticCount != 1 {
		t.Fatal("missing or duplicate HTTP diagnostic", diagnosticCount)
	}
	if exported {
		signals.check(t, instance, privateValues)
	}
}

type httpDiagnosticCollector struct {
	workers     *workerSignalEvidence
	unavailable atomic.Bool
	traces      *workflowTraceCollector
	logs        *workflowLogCollector
	metrics     *workflowMetricCollector
}

func newHTTPDiagnosticCollector(t *testing.T) (*httpDiagnosticCollector, []string) {
	t.Helper()
	return newHTTPDiagnosticCollectorAt(t, "localhost", "127.0.0.1:0")
}

func newHTTPDiagnosticCollectorAt(t *testing.T, hostname, address string) (*httpDiagnosticCollector, []string) {
	t.Helper()
	cert := identity(t, hostname)
	directory := filepath.Dir(cert.config.CAFile)
	pair, err := tls.LoadX509KeyPair(filepath.Join(directory, "server.pem"), filepath.Join(directory, "server-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	signals := &httpDiagnosticCollector{
		traces:  &workflowTraceCollector{received: make(chan *tracecollector.ExportTraceServiceRequest, 64)},
		logs:    &workflowLogCollector{received: make(chan *logcollector.ExportLogsServiceRequest, 64)},
		metrics: &workflowMetricCollector{received: make(chan *metriccollector.ExportMetricsServiceRequest, 64)},
	}
	if os.Getenv("STEGO_TEST_BROWSER_WORKLOAD") == "1" {
		signals.workers = &workerSignalEvidence{}
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}})), grpc.UnaryInterceptor(func(ctx context.Context, request any, info *grpc.UnaryServerInfo, next grpc.UnaryHandler) (any, error) {
		if signals.unavailable.Load() {
			return nil, status.Error(codes.Unavailable, "private-collector-fault")
		}
		if signals.workers != nil {
			if response, handled := signals.workers.collect(request); handled {
				return response, nil
			}
		}
		return next(ctx, request)
	}))
	tracecollector.RegisterTraceServiceServer(server, signals.traces)
	logcollector.RegisterLogsServiceServer(server, signals.logs)
	metriccollector.RegisterMetricsServiceServer(server, signals.metrics)
	go server.Serve(listener)
	t.Cleanup(server.Stop)
	return signals, []string{"OTEL_EXPORTER_OTLP_ENDPOINT=https://" + listener.Addr().String(), "OTEL_EXPORTER_OTLP_CERTIFICATE=" + cert.config.CAFile, "OTEL_TRACES_SAMPLER_ARG=1", "OTEL_METRIC_EXPORT_INTERVAL=1000"}
}

func (c *httpDiagnosticCollector) check(t *testing.T, instance string, private []string) {
	t.Helper()
	checkPrivate := func(message proto.Message) {
		data, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range private {
			if bytes.Contains(data, []byte(value)) {
				t.Fatal("OTLP diagnostic exposed private data", value)
			}
		}
	}
	diagnostics, panicLogs, panicSpans := 0, 0, 0
	var panicLogSpan, panicTraceSpan []byte
	var panicCount uint64
	traces, logs, metrics := c.traces.received, c.logs.received, c.metrics.received
	for traces != nil || logs != nil || metrics != nil {
		select {
		case batch := <-traces:
			checkPrivate(batch)
			for _, resource := range batch.ResourceSpans {
				if telemetryInstance(t, resource.Resource.Attributes, "hypershell-http-diagnostics") != instance {
					t.Fatal("trace resource differs from diagnostic")
				}
				for _, scope := range resource.ScopeSpans {
					for _, span := range scope.Spans {
						if signalAttribute(span.Attributes, "error.type").GetStringValue() == "panic" {
							panicSpans++
							panicTraceSpan = span.SpanId
							if span.Status.GetCode() != 2 || signalAttribute(span.Attributes, "http.response.status_code") != nil {
								t.Fatal("panic trace invented a response status")
							}
						}
					}
				}
			}
		case batch := <-logs:
			checkPrivate(batch)
			for _, resource := range batch.ResourceLogs {
				if telemetryInstance(t, resource.Resource.Attributes, "hypershell-http-diagnostics") != instance {
					t.Fatal("log resource differs from diagnostic")
				}
				for _, scope := range resource.ScopeLogs {
					for _, record := range scope.LogRecords {
						if record.EventName == "http.server.diagnostic" {
							diagnostics++
							if record.Body.GetStringValue() != "HTTP server reported a diagnostic" || record.SeverityNumber != 17 || len(record.Attributes) != 0 || len(record.TraceId) != 0 || len(record.SpanId) != 0 {
								t.Fatal("invalid server diagnostic export")
							}
						}
						if record.EventName == "http.server.request.completed" && signalAttribute(record.Attributes, "error.type").GetStringValue() == "panic" {
							panicLogs++
							panicLogSpan = record.SpanId
						}
					}
				}
			}
		case batch := <-metrics:
			checkPrivate(batch)
			for _, resource := range batch.ResourceMetrics {
				if telemetryInstance(t, resource.Resource.Attributes, "hypershell-http-diagnostics") != instance {
					t.Fatal("metric resource differs from diagnostic")
				}
				for _, scope := range resource.ScopeMetrics {
					for _, metric := range scope.Metrics {
						if metric.Name == "http.server.request.duration" {
							for _, point := range metric.GetHistogram().GetDataPoints() {
								if signalAttribute(point.Attributes, "error.type").GetStringValue() == "panic" && point.Count > panicCount {
									panicCount = point.Count
								}
							}
						}
					}
				}
			}
		default:
			traces, logs, metrics = nil, nil, nil
		}
	}
	if diagnostics != 1 || panicLogs != 1 || panicSpans != 1 || panicCount != 1 || len(panicLogSpan) == 0 || !bytes.Equal(panicLogSpan, panicTraceSpan) {
		t.Fatal("missing or uncorrelated panic signals", diagnostics, panicLogs, panicSpans, panicCount)
	}
}
