package acceptance

import (
	"context"
	"encoding/binary"
	"fmt"
	"strings"
	"testing"
	"time"

	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func deployedSignalFixture(instance int) (*tracecollector.ExportTraceServiceRequest, *logcollector.ExportLogsServiceRequest, *metriccollector.ExportMetricsServiceRequest) {
	service, spanName, event, metric := "hypershell-deployment", "GET /gateways/{id}", "http.server.request.completed", "http.server.request.duration"
	kind := tracepb.Span_SPAN_KIND_SERVER
	if instance >= 3 {
		service, spanName, event, metric, kind = "hypershell-gateway-identity", "controller.reconcile", "controller.work.completed", "stego.controller.work.duration", tracepb.Span_SPAN_KIND_INTERNAL
	}
	attr := func(key, value string) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
	}
	resource := &resourcepb.Resource{Attributes: []*commonpb.KeyValue{attr("service.name", service), attr("service.instance.id", fmt.Sprintf("00000000-0000-4000-8000-%012d", instance))}}
	trace, span := make([]byte, 16), make([]byte, 8)
	trace[0], span[0] = byte(instance), byte(instance)
	traces := &tracecollector.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: resource, ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Name: spanName, Kind: kind, TraceId: trace, SpanId: span}}}}}}}
	logs := &logcollector.ExportLogsServiceRequest{ResourceLogs: []*logpb.ResourceLogs{{Resource: resource, ScopeLogs: []*logpb.ScopeLogs{{LogRecords: []*logpb.LogRecord{{EventName: event, TraceId: trace, SpanId: span, Attributes: []*commonpb.KeyValue{attr("operation", "reconcile")}}}}}}}}
	metrics := &metriccollector.ExportMetricsServiceRequest{ResourceMetrics: []*metricpb.ResourceMetrics{{Resource: resource, ScopeMetrics: []*metricpb.ScopeMetrics{{Metrics: []*metricpb.Metric{{Name: metric, Data: &metricpb.Metric_Histogram{Histogram: &metricpb.Histogram{DataPoints: []*metricpb.HistogramDataPoint{{Count: 1}}}}}}}}}}}
	return traces, logs, metrics
}

func TestDeployedSignalCollectorBoundedQueue(t *testing.T) {
	c := &workflowTraceCollector{received: make(chan *tracecollector.ExportTraceServiceRequest, 64)}
	traces, _, _ := deployedSignalFixture(1)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	for i := 0; i < 64; i++ {
		if _, err := c.Export(ctx, traces); err != nil {
			t.Fatal(err)
		}
	}
	cancel()
	if _, err := c.Export(ctx, traces); err != context.Canceled {
		t.Fatal("a full collector queue must wait for a reader", err)
	}
	if len(c.received) != 64 {
		t.Fatal("the queue did not preserve its bound")
	}
}

func TestDeployedSignalReaderKeepsReplacementSignals(t *testing.T) {
	signals := &httpDiagnosticCollector{
		traces:  &workflowTraceCollector{received: make(chan *tracecollector.ExportTraceServiceRequest, 64)},
		logs:    &workflowLogCollector{received: make(chan *logcollector.ExportLogsServiceRequest, 64)},
		metrics: &workflowMetricCollector{received: make(chan *metriccollector.ExportMetricsServiceRequest, 64)},
	}
	// The first response supplies the Gateway ID before the reader starts.
	first, _, _ := deployedSignalFixture(1)
	signals.traces.received <- first
	reader := startDeployedSignalReader(signals, []string{"private-fixture-value"})
	t.Cleanup(reader.stop)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for instance := 1; instance <= 4; instance++ {
		traces, logs, metrics := deployedSignalFixture(instance)
		// Each stream crosses the old queue limit before the next stream starts.
		tracesFirst := func() {
			for i := 0; i < 65; i++ {
				if _, err := signals.traces.Export(ctx, traces); err != nil {
					t.Fatal("trace export blocked", err)
				}
			}
		}
		logsFirst := func() {
			for i := 0; i < 65; i++ {
				if _, err := signals.logs.Export(ctx, logs); err != nil {
					t.Fatal("log export blocked", err)
				}
			}
		}
		if instance%2 == 0 {
			logsFirst()
			tracesFirst()
		} else {
			tracesFirst()
			logsFirst()
		}

		for i := 0; i < 65; i++ {
			if _, err := signals.metrics.Export(ctx, metrics); err != nil {
				t.Fatal("metric export blocked", err)
			}
		}
	}
	reader.stop()
	if !reader.evidence.complete() || reader.evidence.batches != 781 {
		t.Fatal("replacement evidence was lost", reader.evidence.failure, reader.evidence.batches)
	}
	for _, state := range reader.evidence.instances {
		if len(state.pending) != 0 {
			t.Fatal("completed correlation retained pending IDs")
		}
	}
}

func TestDeployedSignalEvidenceRejectsInvalid(t *testing.T) {
	for _, mode := range []string{"private-trace", "private-log", "private-metric", "extra-instance", "crossed-service", "missing-identity", "nil-resource", "oversize", "zero-correlation", "correlation-limit"} {
		t.Run(mode, func(t *testing.T) {
			e := deployedSignalEvidence{private: []string{"private-fixture-value"}}
			for i := 1; i <= 4; i++ {
				traces, logs, metrics := deployedSignalFixture(i)
				e.collect(traces)
				e.collect(logs)
				e.collect(metrics)
			}
			if !e.complete() {
				t.Fatal("valid evidence was incomplete")
			}
			traces, logs, metrics := deployedSignalFixture(1)
			var message proto.Message = traces
			switch mode {
			case "private-trace":
				traces.ResourceSpans[0].ScopeSpans[0].Spans[0].Name = "private-fixture-value"
			case "private-log":
				logs.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Body = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "private-fixture-value"}}
				message = logs
			case "private-metric":
				metrics.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Description = "private-fixture-value"
				message = metrics
			case "extra-instance":
				traces, _, _ = deployedSignalFixture(5)
				message = traces
			case "crossed-service":
				traces.ResourceSpans[0].Resource.Attributes[0].Value = &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "hypershell-gateway-identity"}}
			case "missing-identity":
				traces.ResourceSpans[0].Resource.Attributes = traces.ResourceSpans[0].Resource.Attributes[:1]
			case "nil-resource":
				traces.ResourceSpans[0].Resource = nil
			case "oversize":
				traces.ResourceSpans[0].ScopeSpans[0].Spans[0].Name = strings.Repeat("x", (1<<20)+1)
			case "zero-correlation":
				traces.ResourceSpans[0].ScopeSpans[0].Spans[0].TraceId = make([]byte, 16)
			case "correlation-limit":
				e = deployedSignalEvidence{}
				for i := 0; i <= deployedSignalPendingLimit; i++ {
					traces, _, _ = deployedSignalFixture(1)
					binary.BigEndian.PutUint64(traces.ResourceSpans[0].ScopeSpans[0].Spans[0].SpanId, uint64(i+1))
					e.collect(traces)
				}
				message = nil
			}
			if message != nil {
				e.collect(message)
			}
			if e.failure == "" || e.complete() {
				t.Fatal("invalid evidence was accepted")
			}
			if len(e.instances) > 4 {
				t.Fatal("instance bound was exceeded")
			}
			for _, state := range e.instances {
				if len(state.pending) > deployedSignalPendingLimit {
					t.Fatal("correlation bound was exceeded")
				}
			}
			// A later valid batch cannot erase a recorded failure.
			traces, _, _ = deployedSignalFixture(1)
			e.collect(traces)
			if e.complete() || strings.Contains(e.failure, "private-fixture-value") {
				t.Fatal("failure was erased or exposed private data")
			}
		})
	}
}

func TestDeployedSignalEvidenceRequiresEachSignal(t *testing.T) {
	for _, missing := range []string{"trace", "log", "metric", "correlation", "positive-count", "worker-operation"} {
		t.Run(missing, func(t *testing.T) {
			e := deployedSignalEvidence{}
			for i := 1; i <= 4; i++ {
				traces, logs, metrics := deployedSignalFixture(i)
				if i == 4 {
					switch missing {
					case "correlation":
						logs.ResourceLogs[0].ScopeLogs[0].LogRecords[0].SpanId = []byte{9, 9, 9, 9, 9, 9, 9, 9}
					case "positive-count":
						metrics.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].GetHistogram().DataPoints[0].Count = 0
					case "worker-operation":
						logs.ResourceLogs[0].ScopeLogs[0].LogRecords[0].Attributes = nil
					}
				}
				if i != 4 || missing != "trace" {
					e.collect(traces)
				}
				if i != 4 || missing != "log" {
					e.collect(logs)
				}
				if i != 4 || missing != "metric" {
					e.collect(metrics)
				}
			}
			if e.complete() {
				t.Fatal("incomplete evidence was accepted")
			}
		})
	}
}
