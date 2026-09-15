package acceptance

import (
	"fmt"
	"testing"

	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

func collectWorkerInstance(t *testing.T, evidence *workerSignalEvidence, service, id string, metric, correlated bool) {
	t.Helper()
	resource := &resourcepb.Resource{Attributes: []*commonpb.KeyValue{
		{Key: "service.name", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: service}}},
		{Key: "service.instance.id", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: id}}},
	}}
	trace, span := make([]byte, 16), make([]byte, 8)
	trace[0], span[0] = 1, 2
	scope := &commonpb.InstrumentationScope{Name: "stego/controller"}
	requests := []any{
		&logcollector.ExportLogsServiceRequest{ResourceLogs: []*logpb.ResourceLogs{{Resource: resource, ScopeLogs: []*logpb.ScopeLogs{{Scope: scope, LogRecords: []*logpb.LogRecord{{EventName: "controller.work.completed", TraceId: trace, SpanId: span}}}}}}},
	}
	if correlated {
		requests = append(requests, &tracecollector.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: resource, ScopeSpans: []*tracepb.ScopeSpans{{Scope: scope, Spans: []*tracepb.Span{{Name: "controller.work", TraceId: trace, SpanId: span}}}}}}})
	}
	if metric {
		requests = append(requests, &metriccollector.ExportMetricsServiceRequest{ResourceMetrics: []*metricpb.ResourceMetrics{{Resource: resource, ScopeMetrics: []*metricpb.ScopeMetrics{{Scope: scope, Metrics: []*metricpb.Metric{{Name: "stego.controller.work.duration"}}}}}}})
	}
	for _, request := range requests {
		if _, handled := evidence.collect(request); !handled {
			t.Fatal("worker signal was not collected")
		}
	}
}

func TestWorkerSignalEvidenceRequiresExactProfileAndEachInstance(t *testing.T) {
	for _, test := range []struct {
		name                 string
		public               bool
		allocation, identity int
		workload             int
		missingMetric        bool
		missingCorrelation   bool
		ready, invalid       bool
	}{
		{name: "internal", allocation: 2, identity: 2, workload: 2, ready: true},
		{name: "public", public: true, allocation: 2, identity: 2, workload: 4, ready: true},
		{name: "missing public restart", public: true, allocation: 2, identity: 2, workload: 3},
		{name: "extra internal restart", allocation: 2, identity: 2, workload: 3},
		{name: "extra allocation restart", public: true, allocation: 3, identity: 2, workload: 4},
		{name: "missing worker", public: true, allocation: 2, workload: 4},
		{name: "missing metric", public: true, allocation: 2, identity: 2, workload: 4, missingMetric: true},
		{name: "missing correlation", public: true, allocation: 2, identity: 2, workload: 4, missingCorrelation: true},
		{name: "collection bound", public: true, allocation: 2, identity: 2, workload: 5, invalid: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			evidence := &workerSignalEvidence{}
			for _, worker := range []struct {
				name  string
				count int
			}{{"hypershell-namespace-allocation", test.allocation}, {"hypershell-gateway-identity", test.identity}, {"hypershell-gateway-workload", test.workload}} {
				for i := 0; i < worker.count; i++ {
					last := worker.name == "hypershell-gateway-workload" && i == worker.count-1
					collectWorkerInstance(t, evidence, worker.name, fmt.Sprintf("instance-%d", i), !last || !test.missingMetric, !last || !test.missingCorrelation)
				}
			}
			ready, invalid, counts := evidence.controllerStatus(test.public)
			if ready != test.ready || invalid != test.invalid {
				t.Fatal("unexpected worker evidence result", ready, invalid, counts)
			}
			for _, count := range counts {
				if count > 4 {
					t.Fatal("worker collection exceeded its bound")
				}
			}
		})
	}
}
