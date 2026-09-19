package acceptance

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type capacityTraceSummary struct {
	Count        int     `json:"count"`
	TotalSeconds float64 `json:"total_seconds"`
	MaxSeconds   float64 `json:"max_seconds"`
}

type capacityDiagnostics struct {
	tracecollector.UnimplementedTraceServiceServer
	mu            sync.Mutex
	accepted      uint64
	traces        map[string]capacityTraceSummary
	dropped       int
	logs, metrics int
}

// Only fixed operation names and outcome classes enter the result. Raw spans,
// attributes, trace IDs, log bodies, and provider responses are never retained.
func (d *capacityDiagnostics) Export(_ context.Context, request *tracecollector.ExportTraceServiceRequest) (*tracecollector.ExportTraceServiceResponse, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, resource := range request.ResourceSpans {
		service := signalAttribute(resource.Resource.GetAttributes(), "service.name").GetStringValue()
		if service != "capacity-api" && service != "capacity-provider" {
			continue
		}
		for _, scope := range resource.ScopeSpans {
			for _, span := range scope.Spans {
				if d.accepted == 0 || span.StartTimeUnixNano < d.accepted || span.EndTimeUnixNano < span.StartTimeUnixNano {
					continue
				}
				name := span.Name
				if index := strings.LastIndexByte(name, '/'); index >= 0 {
					name = name[index+1:]
				}
				switch name {
				case "controller.reconcile", "controller.scan", "GET", "POST", "PUT", "DELETE", "HTTP POST", "HTTP DELETE", "Delete", "DeleteManaged", "DeleteGateway", "LoadServiceAccountProviderState", "SaveServiceAccountProviderState", "Source", "Page", "PrepareCandidate":
				default:
					name = "other"
				}
				outcome := signalAttribute(span.Attributes, "outcome").GetStringValue()
				switch outcome {
				case "success", "failure", "timeout", "canceled":
				default:
					outcome = "other"
				}
				errType := signalAttribute(span.Attributes, "error.type").GetStringValue()
				switch errType {
				case "", "deadline", "canceled", "transport", "capacity", "response", "aborted", "404", "403", "401", "429", "500", "503", "DeadlineExceeded", "Canceled", "Unavailable", "PermissionDenied", "Unauthenticated":
				default:
					errType = capacityRPCStatus(errType)
				}
				rpcStatus := capacityRPCStatus(signalAttribute(span.Attributes, "rpc.response.status_code").GetStringValue())
				bucket := int((span.StartTimeUnixNano - d.accepted) / uint64(10*time.Second))
				if bucket > 15 {
					continue
				}
				key := fmt.Sprintf("%s|%s|%s|%s|%s|%s|%d", service, span.Kind.String(), name, outcome, errType, rpcStatus, bucket)
				row, exists := d.traces[key]
				if !exists && len(d.traces) >= 1024 {
					d.dropped++
					continue
				}
				seconds := float64(span.EndTimeUnixNano-span.StartTimeUnixNano) / 1e9
				row.Count++
				row.TotalSeconds += seconds
				row.MaxSeconds = max(row.MaxSeconds, seconds)
				d.traces[key] = row
			}
		}
	}
	return &tracecollector.ExportTraceServiceResponse{}, nil
}

// Keep the canonical RPC classes emitted by the generated client and server.
// Unknown input must not enter a result key.
func capacityRPCStatus(value string) string {
	switch value {
	case "", "OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED",
		"NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED",
		"FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED",
		"INTERNAL", "UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED":
		return value
	default:
		return "other"
	}
}

type capacityDiagnosticLogs struct {
	logcollector.UnimplementedLogsServiceServer
	d *capacityDiagnostics
}

func (l *capacityDiagnosticLogs) Export(_ context.Context, request *logcollector.ExportLogsServiceRequest) (*logcollector.ExportLogsServiceResponse, error) {
	l.d.mu.Lock()
	defer l.d.mu.Unlock()
	for _, resource := range request.ResourceLogs {
		for _, scope := range resource.ScopeLogs {
			l.d.logs += len(scope.LogRecords)
		}
	}
	return &logcollector.ExportLogsServiceResponse{}, nil
}

type capacityDiagnosticMetrics struct {
	metriccollector.UnimplementedMetricsServiceServer
	d *capacityDiagnostics
}

func (m *capacityDiagnosticMetrics) Export(_ context.Context, request *metriccollector.ExportMetricsServiceRequest) (*metriccollector.ExportMetricsServiceResponse, error) {
	m.d.mu.Lock()
	defer m.d.mu.Unlock()
	for _, resource := range request.ResourceMetrics {
		for _, scope := range resource.ScopeMetrics {
			m.d.metrics += len(scope.Metrics)
		}
	}
	return &metriccollector.ExportMetricsServiceResponse{}, nil
}

func startCapacityDiagnostics(t *testing.T, result map[string]any) (*capacityDiagnostics, []string) {
	t.Helper()
	identity := identity(t, "127.0.0.1")
	directory := filepath.Dir(identity.config.CAFile)
	pair, err := tls.LoadX509KeyPair(filepath.Join(directory, "server.pem"), filepath.Join(directory, "server-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	token := hex.EncodeToString(makeRandom(t, 32))
	tokenFile := filepath.Join(t.TempDir(), "collector-token")
	if err := os.WriteFile(tokenFile, []byte(token), 0600); err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}})), grpc.MaxRecvMsgSize(1<<20), grpc.MaxConcurrentStreams(4), grpc.UnaryInterceptor(func(ctx context.Context, request any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		incoming, _ := metadata.FromIncomingContext(ctx)
		values := incoming.Get("authorization")
		if len(values) != 1 || subtle.ConstantTimeCompare([]byte(values[0]), []byte("Bearer "+token)) != 1 {
			return nil, status.Error(codes.Unauthenticated, "collector identity is required")
		}
		return handler(ctx, request)
	}))
	d := &capacityDiagnostics{traces: map[string]capacityTraceSummary{}}
	tracecollector.RegisterTraceServiceServer(server, d)
	logcollector.RegisterLogsServiceServer(server, &capacityDiagnosticLogs{d: d})
	metriccollector.RegisterMetricsServiceServer(server, &capacityDiagnosticMetrics{d: d})
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		if err := <-done; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Error("diagnostic collector stopped", err)
		}
		d.mu.Lock()
		defer d.mu.Unlock()
		result["diagnostic_trace_summary_version"] = 2
		result["diagnostic_traces"] = d.traces
		result["diagnostic_trace_groups_dropped"] = d.dropped
		result["diagnostic_log_records_received"], result["diagnostic_metrics_received"] = d.logs, d.metrics
	})
	result["diagnostics_enabled"] = true
	return d, []string{"OTEL_EXPORTER_OTLP_ENDPOINT=https://" + listener.Addr().String(), "OTEL_EXPORTER_OTLP_CERTIFICATE=" + identity.config.CAFile, "STEGO_OTEL_TOKEN_FILE=" + tokenFile, "OTEL_TRACES_SAMPLER_ARG=1", "OTEL_METRIC_EXPORT_INTERVAL=1000"}
}

func capacityCleanupSample(t *testing.T, ctx context.Context, f *fixture, id string, accepted time.Time) map[string]any {
	t.Helper()
	call, done := context.WithTimeout(ctx, time.Second)
	defer done()
	result := map[string]any{"seconds": time.Since(accepted).Seconds()}
	var closed int
	if err := f.db.QueryRowContext(call, "SELECT count(*) FROM service_accounts WHERE gateway_id=$1 AND deleted_at IS NOT NULL", id).Scan(&closed); err != nil {
		t.Fatal("read cleanup progress", err)
	}
	result["closed_accounts"] = closed
	for _, name := range []string{"gateway-account-cleanup", "gateway-provider-inventory"} {
		checkpoint, err := f.storage.LoadCheckpoint(call, "Gateway", id, name)
		if err != nil {
			t.Fatal("read cleanup checkpoint", err)
		}
		cycle, err := runtime.DecodeCycle(checkpoint.After)
		if err != nil {
			t.Fatal("decode cleanup checkpoint", err)
		}
		digest := sha256.Sum256([]byte(cycle.Source + "\n" + cycle.After))
		result[name] = map[string]any{"version": checkpoint.Version, "complete": cycle.Complete, "failed": cycle.Failed, "cursor_present": cycle.After != "", "position_sha256": hex.EncodeToString(digest[:])}
	}
	return result
}

// The diagnostic record must reject private text and retain only bounded totals.
func TestCapacityDiagnosticsSummary(t *testing.T) {
	t.Run("rpc_status", testCapacityDiagnosticsRPCStatus)
	const accepted = uint64(time.Minute)
	d := &capacityDiagnostics{accepted: accepted, traces: map[string]capacityTraceSummary{}}
	attr := func(key, value string) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
	}
	spans := []*tracepb.Span{
		{Name: "private-name", StartTimeUnixNano: accepted, EndTimeUnixNano: accepted + uint64(time.Second), Attributes: []*commonpb.KeyValue{attr("outcome", "private-outcome"), attr("error.type", "private-error"), attr("credential", "private-secret"), attr("rpc.response.status_code", "private-status")}},
		{Name: "controller.scan", StartTimeUnixNano: accepted, EndTimeUnixNano: accepted + uint64(2*time.Second), Attributes: []*commonpb.KeyValue{attr("outcome", "timeout")}},
		{Name: "GET", StartTimeUnixNano: accepted - 1, EndTimeUnixNano: accepted},
		{Name: "GET", StartTimeUnixNano: accepted + 1, EndTimeUnixNano: accepted},
		{Name: "GET", StartTimeUnixNano: accepted + uint64(160*time.Second), EndTimeUnixNano: accepted + uint64(161*time.Second)},
	}
	request := &tracecollector.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{attr("service.name", "capacity-api")}}, ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}}}}}
	if _, err := d.Export(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(d.traces) != 2 {
		t.Fatal("invalid diagnostic time filter", len(d.traces))
	}
	if row := d.traces["capacity-api|SPAN_KIND_UNSPECIFIED|other|other|other|other|0"]; row.Count != 1 || row.TotalSeconds != 1 {
		t.Fatal("unknown text must use the fixed other class", row)
	}
	row := d.traces["capacity-api|SPAN_KIND_UNSPECIFIED|controller.scan|timeout|||0"]
	if row.Count != 1 || row.TotalSeconds != 2 || row.MaxSeconds != 2 {
		t.Fatal("invalid diagnostic duration", row)
	}
	data, err := json.Marshal(d.traces)
	if err != nil || strings.Contains(string(data), "private") {
		t.Fatal("private text entered the diagnostic result")
	}
	for len(d.traces) < 1024 {
		d.traces[fmt.Sprint(len(d.traces))] = capacityTraceSummary{}
	}
	spans[0].Name = "DELETE"
	if _, err := d.Export(context.Background(), request); err != nil {
		t.Fatal(err)
	}
	if len(d.traces) != 1024 || d.dropped != 1 {
		t.Fatal("diagnostic group limit failed")
	}
	if d.traces["capacity-api|SPAN_KIND_UNSPECIFIED|controller.scan|timeout|||0"].Count != 2 {
		t.Fatal("existing groups must continue at the limit")
	}
}

// Exercise the collector with the client and server wire values. A successful
// RPC must remain distinct from a span with no RPC status.
func testCapacityDiagnosticsRPCStatus(t *testing.T) {
	attr := func(key, value string) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
	}
	for _, kind := range []tracepb.Span_SpanKind{tracepb.Span_SPAN_KIND_CLIENT, tracepb.Span_SPAN_KIND_SERVER} {
		for _, name := range []string{"OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED"} {
			t.Run(kind.String()+"/"+name, func(t *testing.T) {
				const accepted = uint64(time.Minute)
				d := &capacityDiagnostics{accepted: accepted, traces: map[string]capacityTraceSummary{}}
				errorType := name
				if name == "OK" {
					errorType = ""
				}
				spans := []*tracepb.Span{
					{Name: "/private.package/LoadServiceAccountProviderState", Kind: kind, StartTimeUnixNano: accepted, EndTimeUnixNano: accepted + uint64(time.Second), Attributes: []*commonpb.KeyValue{attr("rpc.response.status_code", name), attr("error.type", errorType), attr("private", "private-secret")}},
					{Name: "/private.package/LoadServiceAccountProviderState", Kind: kind, StartTimeUnixNano: accepted, EndTimeUnixNano: accepted + uint64(2*time.Second)},
				}
				request := &tracecollector.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{attr("service.name", "capacity-api")}}, ScopeSpans: []*tracepb.ScopeSpans{{Spans: spans}}}}}
				if _, err := d.Export(context.Background(), request); err != nil {
					t.Fatal(err)
				}
				key := fmt.Sprintf("capacity-api|%s|LoadServiceAccountProviderState|other|%s|%s|0", kind, errorType, name)
				row := d.traces[key]
				if len(d.traces) != 2 || row.Count != 1 || row.TotalSeconds != 1 || row.MaxSeconds != 1 {
					t.Fatal("RPC status was lost or combined with a missing status", d.traces)
				}
				data, err := json.Marshal(d.traces)
				if err != nil || strings.Contains(string(data), "private") {
					t.Fatal("private text entered the RPC diagnostic result")
				}
			})
		}
	}
}
