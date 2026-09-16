package acceptance

import (
	"bytes"
	"encoding/hex"
	"sync"
	"testing"
	"time"

	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

type dashboardSignalEvidence struct {
	sync.Mutex
	traces, logs    map[string]bool
	metric, invalid bool
}

// Consume only this dashboard's resource. Management console and worker
// evidence must remain separate. Retain bounded IDs, never complete payloads.
func (d *dashboardSignalEvidence) collect(request any) (any, bool) {
	var resources []*resourcepb.Resource
	var response any
	switch batch := request.(type) {
	case *tracecollector.ExportTraceServiceRequest:
		for _, r := range batch.ResourceSpans {
			resources = append(resources, r.Resource)
		}
		response = &tracecollector.ExportTraceServiceResponse{}
	case *logcollector.ExportLogsServiceRequest:
		for _, r := range batch.ResourceLogs {
			resources = append(resources, r.Resource)
		}
		response = &logcollector.ExportLogsServiceResponse{}
	case *metriccollector.ExportMetricsServiceRequest:
		for _, r := range batch.ResourceMetrics {
			resources = append(resources, r.Resource)
		}
		response = &metriccollector.ExportMetricsServiceResponse{}
	default:
		return nil, false
	}
	if len(resources) == 0 {
		return nil, false
	}
	for _, resource := range resources {
		if signalAttribute(resource.GetAttributes(), "service.name").GetStringValue() != "hypershell-gateway-console" {
			return nil, false
		}
	}
	d.Lock()
	defer d.Unlock()
	if d.traces == nil {
		d.traces, d.logs = map[string]bool{}, map[string]bool{}
	}
	validRoute := func(route string) bool {
		switch route {
		case "/", "/gateway", "/global-policy", "/settings", "/workspaces", "/workspaces/{id}", "/workspaces/{id}/sandboxes/{id}", "/workspaces/{id}/providers/{id}":
			return true
		}
		d.invalid = true
		return false
	}
	record := func(target map[string]bool, trace, span []byte, route string) {
		if !validRoute(route) {
			return
		}
		if len(trace) != 16 || len(span) != 8 || bytes.Equal(trace, make([]byte, 16)) || bytes.Equal(span, make([]byte, 8)) {
			d.invalid = true
			return
		}
		if route != "/workspaces/{id}" {
			return
		}
		key := hex.EncodeToString(trace) + hex.EncodeToString(span)
		if len(target) >= 32 && !target[key] {
			d.invalid = true
			return
		}
		target[key] = true
	}
	switch batch := request.(type) {
	case *tracecollector.ExportTraceServiceRequest:
		for _, resource := range batch.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					if span.Name == "dashboard.document.rendered" {
						record(d.traces, span.TraceId, span.SpanId, signalAttribute(span.Attributes, "http.route").GetStringValue())
					}
				}
			}
		}
	case *logcollector.ExportLogsServiceRequest:
		for _, resource := range batch.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				for _, entry := range scope.LogRecords {
					if entry.Body.GetStringValue() == "dashboard.document.rendered" {
						record(d.logs, entry.TraceId, entry.SpanId, signalAttribute(entry.Attributes, "http.route").GetStringValue())
					}
				}
			}
		}
	case *metriccollector.ExportMetricsServiceRequest:
		for _, resource := range batch.ResourceMetrics {
			for _, scope := range resource.ScopeMetrics {
				for _, metric := range scope.Metrics {
					if metric.Name != "dashboard.document.views" {
						continue
					}
					for _, point := range metric.GetSum().GetDataPoints() {
						route := signalAttribute(point.Attributes, "http.route").GetStringValue()
						if validRoute(route) && route == "/workspaces/{id}" && (point.GetAsInt() > 0 || point.GetAsDouble() > 0) {
							d.metric = true
						}
					}
				}
			}
		}
	}
	return response, true
}

func (d *dashboardSignalEvidence) require(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		d.Lock()
		invalid, ready := d.invalid, false
		for key := range d.traces {
			if d.logs[key] && d.metric {
				ready = true
			}
		}
		d.Unlock()
		if invalid {
			t.Fatal("dashboard signal exceeded its route or evidence limits")
		}
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("dashboard did not deliver correlated logs, traces, and metrics")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
