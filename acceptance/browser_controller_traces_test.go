package acceptance

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

const controllerTracePairLimit = 256

type controllerTracePair struct {
	operation, outcome        string
	retry, log, span, counted bool
}
type controllerTraceState struct {
	pairs   map[string]*controllerTracePair
	owners  map[string]string
	order   []string
	counts  map[string]int
	last    map[string]string
	invalid bool
}

func controllerTraceID(trace, span []byte) string {
	if len(trace) != 16 || len(span) != 8 || bytes.Equal(trace, make([]byte, 16)) || bytes.Equal(span, make([]byte, 8)) {
		return ""
	}
	return hex.EncodeToString(trace) + "/" + hex.EncodeToString(span)
}
func controllerTraceFields(attrs []*commonpb.KeyValue, log bool) (operation, outcome string, retry, valid bool) {
	seen := map[string]bool{}
	for _, a := range attrs {
		if a == nil || a.Value == nil || seen[a.Key] {
			return
		}
		seen[a.Key] = true
		switch a.Key {
		case "operation", "outcome":
			value, ok := a.Value.Value.(*commonpb.AnyValue_StringValue)
			if !ok {
				return
			}
			if a.Key == "operation" {
				operation = value.StringValue
			} else {
				outcome = value.StringValue
			}
		case "retry":
			value, ok := a.Value.Value.(*commonpb.AnyValue_BoolValue)
			if !ok {
				return
			}
			retry = value.BoolValue
		case "duration_seconds":
			value, ok := a.Value.Value.(*commonpb.AnyValue_DoubleValue)
			if !log || !ok || value.DoubleValue < 0 || math.IsNaN(value.DoubleValue) || math.IsInf(value.DoubleValue, 0) {
				return
			}
		default:
			return
		}
	}
	if !seen["operation"] || !seen["outcome"] || !seen["retry"] || seen["duration_seconds"] != log {
		return
	}
	switch operation {
	case "reconcile", "scan", "cleanup", "watch":
	default:
		return
	}
	switch outcome {
	case "success":
		if retry {
			return
		}
	case "pending":
		if retry || operation != "reconcile" {
			return
		}
	case "failure", "canceled", "timeout":
	default:
		return
	}
	return operation, outcome, retry, true
}
func (s *controllerTraceState) record(id, operation, outcome string, retry, log bool) {
	if id == "" {
		s.invalid = true
		return
	}
	if s.pairs == nil {
		s.pairs = map[string]*controllerTracePair{}
		s.owners = map[string]string{}
		s.counts = map[string]int{}
		s.last = map[string]string{}
	}
	value := s.pairs[id]
	if value == nil {
		if len(s.order) == controllerTracePairLimit {
			old := s.order[0]
			s.order = s.order[1:]
			delete(s.pairs, old)
			trace, _, _ := strings.Cut(old, "/")
			if s.owners[trace] == old {
				delete(s.owners, trace)
			}
		}
		value = &controllerTracePair{operation: operation, outcome: outcome, retry: retry}
		s.pairs[id] = value
		s.order = append(s.order, id)
	}
	if value.operation != operation || value.outcome != outcome || value.retry != retry {
		s.invalid = true
		return
	}
	if log {
		value.log = true
	} else {
		trace, _, _ := strings.Cut(id, "/")
		if old := s.owners[trace]; old != "" && old != id {
			s.invalid = true
			return
		}
		s.owners[trace] = id
		value.span = true
	}
	if value.log && value.span && !value.counted {
		value.counted = true
		trace, _, _ := strings.Cut(id, "/")
		if s.last[operation] != trace && s.counts[operation] < 2 {
			s.counts[operation]++
		}
		s.last[operation] = trace
	}
}
func (s *controllerTraceState) log(record *logpb.LogRecord) {
	if record.GetEventName() != "controller.work.completed" {
		return
	}
	operation, outcome, retry, valid := controllerTraceFields(record.Attributes, true)
	if !valid {
		s.invalid = true
		return
	}
	s.record(controllerTraceID(record.TraceId, record.SpanId), operation, outcome, retry, true)
}
func (s *controllerTraceState) span(span *tracepb.Span) {
	operation, outcome, retry, valid := controllerTraceFields(span.GetAttributes(), false)
	if !valid || span.Name != "controller."+operation || span.Kind != tracepb.Span_SPAN_KIND_INTERNAL || len(span.ParentSpanId) != 0 || len(span.Links) != 0 || len(span.Events) != 0 || span.TraceState != "" {
		s.invalid = true
		return
	}
	s.record(controllerTraceID(span.TraceId, span.SpanId), operation, outcome, retry, false)
}

// Every instance must have a validated pair. Repeated work must be proved for
// each service, without requiring a short-lived old instance to run another scan.
func (w *workerSignalEvidence) controllerTraceStatus(public bool, endpointChange ...bool) (ready, invalid bool, evidence map[string]map[string]map[string]int) {
	w.Lock()
	defer w.Unlock()
	ready = len(w.instances) == 3
	invalid = w.invalid
	evidence = map[string]map[string]map[string]int{}
	for name, states := range w.instances {
		ready = ready && len(states) == expectedWorkerInstances(name, public, endpointChange...)
		evidence[name] = map[string]map[string]int{}
		repeated := map[string]bool{}
		for id, state := range states {
			s := &state.controller
			invalid = invalid || s.invalid || !telemetryInstancePattern.MatchString(id)
			counts := map[string]int{}
			matched := false
			for operation, count := range s.counts {
				counts[operation] = count
				matched = matched || count > 0
				repeated[operation] = repeated[operation] || count >= 2
			}
			ready = ready && matched
			evidence[name][id] = counts
		}
		for _, operation := range []string{"reconcile", "scan", "cleanup"} {
			ready = ready && repeated[operation]
		}
	}
	return ready && !invalid, invalid, evidence
}
func (w *workerSignalEvidence) checkControllerTraces(t *testing.T, public bool, endpointChange ...bool) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		ready, invalid, evidence := w.controllerTraceStatus(public, endpointChange...)
		if invalid {
			t.Fatal("controller traces contain invalid ancestry, fields, or identities")
		}
		if ready {
			if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
				record := struct {
					Schema    int                                  `json:"schema"`
					PairLimit int                                  `json:"pair_limit_per_instance"`
					Instances map[string]map[string]map[string]int `json:"instances"`
				}{1, controllerTracePairLimit, evidence}
				body, err := json.MarshalIndent(record, "", "  ")
				if err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(filepath.Join(directory, "controller-traces.json"), append(body, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Log("Each worker instance exported a valid controller root; each service exported separate reconciliation, scan, and cleanup traces")
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("independent controller trace evidence is incomplete", evidence)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
