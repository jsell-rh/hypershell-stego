package acceptance

import (
	"testing"

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

func startupFixture() (*logcollector.ExportLogsServiceRequest, *tracecollector.ExportTraceServiceRequest, *metriccollector.ExportMetricsServiceRequest) {
	attr := func(k, v string) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
	}
	resource := &resourcepb.Resource{Attributes: []*commonpb.KeyValue{attr("service.name", "hypershell-console"), attr("service.instance.id", "11111111-1111-4111-8111-111111111111")}}
	scope := &commonpb.InstrumentationScope{Name: "stego/startup"}
	logs := &logpb.ScopeLogs{Scope: scope}
	spans := &tracepb.ScopeSpans{Scope: scope}
	histogram := &metricpb.Histogram{AggregationTemporality: metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE}
	active := &metricpb.Sum{AggregationTemporality: metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE}
	for i, stage := range browserStartupStages {
		attrs := []*commonpb.KeyValue{attr("startup.stage", stage), attr("outcome", "success")}
		trace, span := make([]byte, 16), make([]byte, 8)
		trace[0] = byte(i + 1)
		span[0] = byte(i + 1)
		logs.LogRecords = append(logs.LogRecords, &logpb.LogRecord{TimeUnixNano: 1, EventName: "startup.step.completed", Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "Startup step completed"}}, SeverityNumber: logpb.SeverityNumber_SEVERITY_NUMBER_INFO, SeverityText: "INFO", TraceId: trace, SpanId: span, Attributes: append(append([]*commonpb.KeyValue{}, attrs...), &commonpb.KeyValue{Key: "duration_seconds", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 0.1}}})})
		spans.Spans = append(spans.Spans, &tracepb.Span{Name: "startup." + stage, Kind: tracepb.Span_SPAN_KIND_INTERNAL, TraceId: trace, SpanId: span, StartTimeUnixNano: 1, EndTimeUnixNano: 2, Attributes: attrs})
		duration := 0.1
		histogram.DataPoints = append(histogram.DataPoints, &metricpb.HistogramDataPoint{Attributes: attrs, Count: 1, Sum: &duration})
		active.DataPoints = append(active.DataPoints, &metricpb.NumberDataPoint{Attributes: []*commonpb.KeyValue{attr("startup.stage", stage)}, Value: &metricpb.NumberDataPoint_AsInt{AsInt: 0}})
	}
	metricScope := &metricpb.ScopeMetrics{Scope: scope, Metrics: []*metricpb.Metric{
		{Name: "stego.startup.duration", Unit: "s", Data: &metricpb.Metric_Histogram{Histogram: histogram}},
		{Name: "stego.startup.active_steps", Unit: "{step}", Data: &metricpb.Metric_Sum{Sum: active}},
	}}
	return &logcollector.ExportLogsServiceRequest{ResourceLogs: []*logpb.ResourceLogs{{Resource: resource, ScopeLogs: []*logpb.ScopeLogs{logs}}}},
		&tracecollector.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: resource, ScopeSpans: []*tracepb.ScopeSpans{spans}}}},
		&metriccollector.ExportMetricsServiceRequest{ResourceMetrics: []*metricpb.ResourceMetrics{{Resource: resource, ScopeMetrics: []*metricpb.ScopeMetrics{metricScope}}}}
}
func TestBrowserStartupEvidenceRequiresCompleteInstance(t *testing.T) {
	logs, spans, metrics := startupFixture()
	var e startupSignalEvidence
	e.collect(metrics)
	e.collect(spans)
	state := e.instances["hypershell-console"]["11111111-1111-4111-8111-111111111111"]
	if startupComplete(state) {
		t.Fatal("missing logs passed")
	}
	other := proto.Clone(logs).(*logcollector.ExportLogsServiceRequest)
	other.ResourceLogs[0].Resource.Attributes[1].Value.Value = &commonpb.AnyValue_StringValue{StringValue: "22222222-2222-4222-8222-222222222222"}
	e.collect(other)
	if startupComplete(state) {
		t.Fatal("another process supplied missing logs")
	}
	e.collect(logs)
	e.collect(logs)
	if e.invalid || !startupComplete(state) || len(state.Pairs) != 8 {
		t.Fatal("complete signals or repeated export failed")
	}
	delete(state.Active, browserStartupStages[0])
	if startupComplete(state) {
		t.Fatal("missing idle stage count passed")
	}
}
func TestBrowserStartupEvidenceRejectsInvalidRecords(t *testing.T) {
	for _, name := range []string{"private_field", "wrong_duration_type", "zero_trace", "duplicate_field", "unknown_stage", "wrong_body", "wrong_severity", "unknown_service", "missing_instance", "wrong_span_name", "private_span_event", "wrong_histogram_unit", "active_count"} {
		t.Run(name, func(t *testing.T) {
			logs, spans, metrics := startupFixture()
			log := logs.ResourceLogs[0].ScopeLogs[0].LogRecords[0]
			span := spans.ResourceSpans[0].ScopeSpans[0].Spans[0]
			switch name {
			case "private_field":
				log.Attributes = append(log.Attributes, &commonpb.KeyValue{Key: "url.full", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "private-value"}}})
			case "wrong_duration_type":
				log.Attributes[2].Value.Value = &commonpb.AnyValue_StringValue{StringValue: "0.1"}
			case "zero_trace":
				log.TraceId = make([]byte, 16)
			case "duplicate_field":
				log.Attributes = append(log.Attributes, log.Attributes[0])
			case "unknown_stage":
				log.Attributes[0].Value.Value = &commonpb.AnyValue_StringValue{StringValue: "private-stage"}
			case "wrong_body":
				log.Body.Value = &commonpb.AnyValue_StringValue{StringValue: "private-error"}
			case "wrong_severity":
				log.SeverityText = "ERROR"
			case "unknown_service":
				logs.ResourceLogs[0].Resource.Attributes[0].Value.Value = &commonpb.AnyValue_StringValue{StringValue: "unrelated-service"}
			case "missing_instance":
				logs.ResourceLogs[0].Resource.Attributes = logs.ResourceLogs[0].Resource.Attributes[:1]
			case "wrong_span_name":
				span.Name = "private-span"
			case "private_span_event":
				span.Events = []*tracepb.Span_Event{{Name: "private-event"}}
			case "wrong_histogram_unit":
				metrics.ResourceMetrics[0].ScopeMetrics[0].Metrics[0].Unit = "ms"
			case "active_count":
				metrics.ResourceMetrics[0].ScopeMetrics[0].Metrics[1].GetSum().DataPoints[0].Value = &metricpb.NumberDataPoint_AsInt{AsInt: -1}
			}
			var e startupSignalEvidence
			e.collect(logs)
			e.collect(spans)
			e.collect(metrics)
			if !e.invalid {
				t.Fatal("invalid startup evidence passed")
			}
		})
	}
}

func TestDashboardEvidenceRequiresRelayIdentity(t *testing.T) {
	attr := func(k, v string) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: k, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: v}}}
	}
	const first = "11111111-1111-4111-8111-111111111111"
	const second = "22222222-2222-4222-8222-222222222222"
	resource := func(id string) *resourcepb.Resource {
		return &resourcepb.Resource{Attributes: []*commonpb.KeyValue{attr("service.name", "hypershell-gateway-console"), attr("stego.relay.service.name", "hypershell-gateway-console"), attr("stego.relay.instance.id", id)}}
	}
	trace, span := make([]byte, 16), make([]byte, 8)
	trace[0] = 1
	span[0] = 1
	attrs := []*commonpb.KeyValue{attr("http.route", "/workspaces/{id}")}
	traces := &tracecollector.ExportTraceServiceRequest{ResourceSpans: []*tracepb.ResourceSpans{{Resource: resource(first), ScopeSpans: []*tracepb.ScopeSpans{{Spans: []*tracepb.Span{{Name: "dashboard.document.rendered", TraceId: trace, SpanId: span, Attributes: attrs}}}}}}}
	logs := &logcollector.ExportLogsServiceRequest{ResourceLogs: []*logpb.ResourceLogs{{Resource: resource(second), ScopeLogs: []*logpb.ScopeLogs{{LogRecords: []*logpb.LogRecord{{Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "dashboard.document.rendered"}}, TraceId: trace, SpanId: span, Attributes: attrs}}}}}}}
	metric := &metricpb.Metric{Name: "dashboard.document.views", Data: &metricpb.Metric_Sum{Sum: &metricpb.Sum{DataPoints: []*metricpb.NumberDataPoint{{Attributes: attrs, Value: &metricpb.NumberDataPoint_AsInt{AsInt: 1}}}}}}
	metrics := &metriccollector.ExportMetricsServiceRequest{ResourceMetrics: []*metricpb.ResourceMetrics{{Resource: resource(first), ScopeMetrics: []*metricpb.ScopeMetrics{{Metrics: []*metricpb.Metric{metric}}}}}}
	var d dashboardSignalEvidence
	d.collect(traces)
	d.collect(logs)
	d.collect(metrics)
	if d.invalid || d.readyInstance() != "" {
		t.Fatal("dashboard evidence mixed relay instances")
	}
	logs.ResourceLogs[0].Resource = resource(first)
	d.collect(logs)
	if d.readyInstance() != first {
		t.Fatal("complete dashboard relay identity is missing")
	}
	logs.ResourceLogs[0].Resource.Attributes = logs.ResourceLogs[0].Resource.Attributes[:1]
	d.collect(logs)
	if !d.invalid {
		t.Fatal("dashboard accepted missing relay identity")
	}
}
