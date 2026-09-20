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
	postgres           postgresSignalState
	controller         controllerTraceState
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
	case "hypershell-namespace-allocation", "hypershell-gateway-identity", "hypershell-gateway-workload":
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
			// Public faults and the endpoint change start at most five instances.
			// Keep collection bounded; check the exact profile count below.
			if len(states) >= 5 {
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
					if scope.Scope.Name == "stego/postgres-client" {
						s.postgres.log(record)
					}
					if scope.Scope.GetName() == "stego/controller" {
						s.controller.log(record)
					}
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
					s.postgres.span(scope.Scope.Name, span)
					if scope.Scope.GetName() == "stego/controller" {
						s.controller.span(span)
					}
					s.pair(pair(span.TraceId, span.SpanId), false)
				}
			}
		}
	case *metriccollector.ExportMetricsServiceRequest:
		for _, r := range batch.ResourceMetrics {
			s := state(r.Resource)
			for _, scope := range r.ScopeMetrics {
				for _, metric := range scope.Metrics {
					if scope.Scope.Name == "stego/postgres-client" {
						s.postgres.metric(metric)
					}
					if metric.Name == "stego.controller.work.duration" {
						s.metric = true
					}
				}
			}
		}
	}
	return response, true
}
func expectedWorkerInstances(name string, public bool, endpointChange ...bool) int {
	count := 2
	if name == "hypershell-gateway-workload" && public {
		count += 2
	}
	if len(endpointChange) == 1 && endpointChange[0] && name != "hypershell-gateway-identity" {
		count++
	}
	return count
}

func (w *workerSignalEvidence) controllerStatus(public bool, endpointChange ...bool) (ready, invalid bool, counts map[string]int) {
	w.Lock()
	defer w.Unlock()
	ready = !w.invalid && len(w.instances) == 3
	counts = map[string]int{}
	for name, states := range w.instances {
		counts[name] = len(states)
		ready = ready && len(states) == expectedWorkerInstances(name, public, endpointChange...)
		for _, s := range states {
			ready = ready && s.metric && s.correlated
		}
	}
	return ready, w.invalid, counts
}

func (w *workerSignalEvidence) check(t *testing.T, public bool, endpointChange ...bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		ready, invalid, counts := w.controllerStatus(public, endpointChange...)
		if ready {
			t.Log("Each expected worker instance exported metrics and correlated logs and traces", counts)
			return
		}
		if invalid || time.Now().After(deadline) {
			t.Fatal("worker telemetry is incomplete; observed instances", counts, "public profile", public, "invalid signals", invalid)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
