package acceptance

import (
	"encoding/hex"
	"sync"
	"testing"
	"time"

	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

type workerSignalState struct {
	logs, traces       []string
	metric, correlated bool
}
type workerSignalEvidence struct {
	sync.Mutex
	instances map[string]map[string]*workerSignalState
	invalid   bool
}

func workerService(resource *resourcepb.Resource) (string, string, bool) {
	name := signalAttribute(resource.GetAttributes(), "service.name").GetStringValue()
	id := signalAttribute(resource.GetAttributes(), "service.instance.id").GetStringValue()
	switch name {
	case "hypershell-database", "hypershell-gateway-identity", "hypershell-gateway-workload":
		return name, id, true
	}
	return "", "", false
}
func (s *workerSignalState) pair(value string, log bool) {
	if value == "" {
		return
	}
	own, other := &s.traces, s.logs
	if log {
		own, other = &s.logs, s.traces
	}
	for _, candidate := range other {
		if candidate == value {
			s.correlated = true
		}
	}
	if len(*own) == 32 {
		*own = (*own)[1:]
	}
	*own = append(*own, value)
}
func (w *workerSignalEvidence) collect(request any) (any, bool) {
	var resources []*resourcepb.Resource
	var response any
	switch batch := request.(type) {
	case *logcollector.ExportLogsServiceRequest:
		for _, r := range batch.ResourceLogs {
			resources = append(resources, r.Resource)
		}
		response = &logcollector.ExportLogsServiceResponse{}
	case *tracecollector.ExportTraceServiceRequest:
		for _, r := range batch.ResourceSpans {
			resources = append(resources, r.Resource)
		}
		response = &tracecollector.ExportTraceServiceResponse{}
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
	for _, r := range resources {
		if _, _, ok := workerService(r); !ok {
			return nil, false
		}
	}
	w.Lock()
	defer w.Unlock()
	state := func(r *resourcepb.Resource) *workerSignalState {
		name, id, _ := workerService(r)
		if id == "" || len(id) > 128 {
			w.invalid = true
			return &workerSignalState{}
		}
		if w.instances == nil {
			w.instances = map[string]map[string]*workerSignalState{}
		}
		if w.instances[name] == nil {
			w.instances[name] = map[string]*workerSignalState{}
		}
		states := w.instances[name]
		if states[id] == nil {
			if len(states) >= 2 {
				w.invalid = true
				return &workerSignalState{}
			}
			states[id] = &workerSignalState{}
		}
		return states[id]
	}
	pair := func(trace, span []byte) string {
		if len(trace) != 16 || len(span) != 8 {
			return ""
		}
		return hex.EncodeToString(trace) + "/" + hex.EncodeToString(span)
	}
	switch batch := request.(type) {
	case *logcollector.ExportLogsServiceRequest:
		for _, r := range batch.ResourceLogs {
			s := state(r.Resource)
			for _, scope := range r.ScopeLogs {
				for _, record := range scope.LogRecords {
					if record.EventName == "controller.work.completed" {
						s.pair(pair(record.TraceId, record.SpanId), true)
					}
				}
			}
		}
	case *tracecollector.ExportTraceServiceRequest:
		for _, r := range batch.ResourceSpans {
			s := state(r.Resource)
			for _, scope := range r.ScopeSpans {
				for _, span := range scope.Spans {
					s.pair(pair(span.TraceId, span.SpanId), false)
				}
			}
		}
	case *metriccollector.ExportMetricsServiceRequest:
		for _, r := range batch.ResourceMetrics {
			s := state(r.Resource)
			for _, scope := range r.ScopeMetrics {
				for _, metric := range scope.Metrics {
					if metric.Name == "stego.controller.work.duration" {
						s.metric = true
					}
				}
			}
		}
	}
	return response, true
}
func (w *workerSignalEvidence) check(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		w.Lock()
		ready := !w.invalid && len(w.instances) == 3
		for _, states := range w.instances {
			ready = ready && len(states) == 2
			for _, s := range states {
				ready = ready && s.metric && s.correlated
			}
		}
		invalid := w.invalid
		w.Unlock()
		if ready {
			t.Log("All three workers exported metrics and correlated logs and traces before and after Pod replacement")
			return
		}
		if invalid || time.Now().After(deadline) {
			t.Fatal("worker telemetry did not prove all six process instances")
		}
		time.Sleep(100 * time.Millisecond)
	}
}
