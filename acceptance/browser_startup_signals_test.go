package acceptance

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
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
)

var browserStartupStages = []string{"browser.transport", "browser.configuration", "browser.session_keys", "browser.session_schema", "browser.upstream", "browser.discovery", "browser.authorization", "browser.routes"}

type startupSignalPair struct{ Log, Span string }
type startupSignalInstance struct {
	Pairs   map[string]startupSignalPair
	Metrics map[string]bool
	Active  map[string]bool
}
type startupSignalEvidence struct {
	sync.Mutex
	instances map[string]map[string]*startupSignalInstance
	invalid   bool
}

func startupStageValid(stage string) bool {
	for _, candidate := range browserStartupStages {
		if candidate == stage {
			return true
		}
	}
	return false
}
func startupSignalKey(attrs []*commonpb.KeyValue, duration bool) (string, bool) {
	seen := map[string]bool{}
	for _, a := range attrs {
		if a == nil || a.Value == nil || seen[a.Key] {
			return "", false
		}
		seen[a.Key] = true
		switch a.Key {
		case "startup.stage", "outcome", "error.type":
			if _, ok := a.Value.Value.(*commonpb.AnyValue_StringValue); !ok {
				return "", false
			}
		case "duration_seconds":
			v, ok := a.Value.Value.(*commonpb.AnyValue_DoubleValue)
			if !duration || !ok || v.DoubleValue < 0 || math.IsNaN(v.DoubleValue) || math.IsInf(v.DoubleValue, 0) {
				return "", false
			}
		default:
			return "", false
		}
	}
	stage := signalAttribute(attrs, "startup.stage").GetStringValue()
	outcome := signalAttribute(attrs, "outcome").GetStringValue()
	if !startupStageValid(stage) || duration != seen["duration_seconds"] {
		return "", false
	}
	switch outcome {
	case "success", "failure", "canceled", "deadline", "aborted":
	default:
		return "", false
	}
	failed := outcome != "success" && outcome != "canceled"
	if failed != seen["error.type"] || failed && signalAttribute(attrs, "error.type").GetStringValue() != outcome {
		return "", false
	}
	return stage + "/" + outcome, true
}
func startupPairID(trace, span []byte) string {
	if len(trace) != 16 || len(span) != 8 || bytes.Equal(trace, make([]byte, 16)) || bytes.Equal(span, make([]byte, 8)) {
		return ""
	}
	return hex.EncodeToString(trace) + "/" + hex.EncodeToString(span)
}
func (e *startupSignalEvidence) state(r *resourcepb.Resource) *startupSignalInstance {
	service := signalAttribute(r.GetAttributes(), "service.name").GetStringValue()
	if service != "hypershell-console" && service != "hypershell-gateway-console" {
		e.invalid = true
		return nil
	}
	id := signalAttribute(r.GetAttributes(), "service.instance.id").GetStringValue()
	if !telemetryInstancePattern.MatchString(id) {
		e.invalid = true
		return nil
	}
	if e.instances == nil {
		e.instances = map[string]map[string]*startupSignalInstance{}
	}
	if e.instances[service] == nil {
		e.instances[service] = map[string]*startupSignalInstance{}
	}
	states := e.instances[service]
	if states[id] == nil {
		if len(states) >= 32 {
			e.invalid = true
			return nil
		}
		states[id] = &startupSignalInstance{Pairs: map[string]startupSignalPair{}, Metrics: map[string]bool{}, Active: map[string]bool{}}
	}
	return states[id]
}
func (e *startupSignalEvidence) pair(s *startupSignalInstance, id, key string, log bool) {
	if id == "" {
		e.invalid = true
		return
	}
	p, exists := s.Pairs[id]
	if !exists && len(s.Pairs) >= 128 {
		e.invalid = true
		return
	}
	if log {
		if p.Log != "" && p.Log != key {
			e.invalid = true
		}
		p.Log = key
	} else {
		if p.Span != "" && p.Span != key {
			e.invalid = true
		}
		p.Span = key
	}
	if p.Log != "" && p.Span != "" && p.Log != p.Span {
		e.invalid = true
	}
	s.Pairs[id] = p
}
func (e *startupSignalEvidence) log(s *startupSignalInstance, r *logpb.LogRecord) {
	key, ok := startupSignalKey(r.Attributes, true)
	outcome := signalAttribute(r.Attributes, "outcome").GetStringValue()
	severity, text := logpb.SeverityNumber_SEVERITY_NUMBER_INFO, "INFO"
	if outcome != "success" && outcome != "canceled" {
		severity, text = logpb.SeverityNumber_SEVERITY_NUMBER_ERROR, "ERROR"
	}
	if !ok || r.EventName != "startup.step.completed" || r.Body.GetStringValue() != "Startup step completed" || r.SeverityNumber != severity || r.SeverityText != text || r.TimeUnixNano == 0 || r.DroppedAttributesCount != 0 {
		e.invalid = true
		return
	}
	e.pair(s, startupPairID(r.TraceId, r.SpanId), key, true)
}
func (e *startupSignalEvidence) span(s *startupSignalInstance, r *tracepb.Span) {
	key, ok := startupSignalKey(r.Attributes, false)
	stage := signalAttribute(r.Attributes, "startup.stage").GetStringValue()
	outcome := signalAttribute(r.Attributes, "outcome").GetStringValue()
	failed := outcome != "success" && outcome != "canceled"
	if !ok || r.Name != "startup."+stage || r.Kind != tracepb.Span_SPAN_KIND_INTERNAL || r.StartTimeUnixNano == 0 || r.EndTimeUnixNano < r.StartTimeUnixNano || r.DroppedAttributesCount != 0 || len(r.Events) != 0 || len(r.Links) != 0 || r.Status.GetMessage() != "" || failed != (r.Status.GetCode() == tracepb.Status_STATUS_CODE_ERROR) {
		e.invalid = true
		return
	}
	e.pair(s, startupPairID(r.TraceId, r.SpanId), key, false)
}
func (e *startupSignalEvidence) metric(s *startupSignalInstance, m *metricpb.Metric) {
	if m.Name == "stego.startup.active_steps" {
		sum := m.GetSum()
		if sum == nil || sum.IsMonotonic || m.Unit != "{step}" || sum.AggregationTemporality != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
			e.invalid = true
			return
		}
		for _, p := range sum.DataPoints {
			stage := signalAttribute(p.Attributes, "startup.stage").GetStringValue()
			value, ok := p.Value.(*metricpb.NumberDataPoint_AsInt)
			if len(p.Attributes) != 1 || !startupStageValid(stage) || !ok || value.AsInt < 0 || value.AsInt > 1 {
				e.invalid = true
				continue
			}
			if value.AsInt == 0 {
				s.Active[stage] = true
			}
		}
		return
	}
	h := m.GetHistogram()
	if m.Name != "stego.startup.duration" || m.Unit != "s" || h == nil || h.AggregationTemporality != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
		e.invalid = true
		return
	}
	for _, p := range h.DataPoints {
		key, ok := startupSignalKey(p.Attributes, false)
		if !ok || p.Count == 0 || p.Sum == nil || p.GetSum() < 0 || math.IsNaN(p.GetSum()) || math.IsInf(p.GetSum(), 0) {
			e.invalid = true
			continue
		}
		s.Metrics[key] = true
	}
}

// Observe after collector authentication. Do not consume other evidence.
// Keep fixed fields and bounded correlation IDs; do not retain OTLP payloads.
func (e *startupSignalEvidence) collect(request any) {
	e.Lock()
	defer e.Unlock()
	switch b := request.(type) {
	case *logcollector.ExportLogsServiceRequest:
		for _, r := range b.ResourceLogs {
			for _, scope := range r.ScopeLogs {
				if scope.Scope.GetName() != "stego/startup" {
					continue
				}
				if s := e.state(r.Resource); s != nil {
					for _, v := range scope.LogRecords {
						e.log(s, v)
					}
				}
			}
		}
	case *tracecollector.ExportTraceServiceRequest:
		for _, r := range b.ResourceSpans {
			for _, scope := range r.ScopeSpans {
				if scope.Scope.GetName() != "stego/startup" {
					continue
				}
				if s := e.state(r.Resource); s != nil {
					for _, v := range scope.Spans {
						e.span(s, v)
					}
				}
			}
		}
	case *metriccollector.ExportMetricsServiceRequest:
		for _, r := range b.ResourceMetrics {
			for _, scope := range r.ScopeMetrics {
				if scope.Scope.GetName() != "stego/startup" {
					continue
				}
				if s := e.state(r.Resource); s != nil {
					for _, v := range scope.Metrics {
						e.metric(s, v)
					}
				}
			}
		}
	}
}
func startupComplete(s *startupSignalInstance) bool {
	if s == nil {
		return false
	}
	for _, stage := range browserStartupStages {
		key := stage + "/success"
		matched := false
		for _, p := range s.Pairs {
			if p.Log == key && p.Span == key {
				matched = true
			}
		}
		if !matched || !s.Metrics[key] || !s.Active[stage] {
			return false
		}
	}
	return true
}
func (e *startupSignalEvidence) require(t *testing.T, service, id string) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		e.Lock()
		ready := false
		for candidate, s := range e.instances[service] {
			if (id == "" || id == candidate) && startupComplete(s) {
				ready = true
			}
		}
		invalid := e.invalid
		e.Unlock()
		if invalid {
			t.Fatal("browser startup signals contain invalid fields or exceed evidence limits")
		}
		if ready {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("browser startup logs, traces, metrics, or idle stage counts are missing", service)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
func (e *startupSignalEvidence) save(t *testing.T) {
	t.Helper()
	e.Lock()
	defer e.Unlock()
	if e.invalid {
		t.Fatal("invalid browser startup signals")
	}
	if dir := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); dir != "" {
		body, err := json.MarshalIndent(e.instances, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(dir, "browser-startup-signals.json"), append(body, '\n'), 0600) != nil {
			t.Fatal("cannot save browser startup evidence")
		}
	}
	t.Log("Generated browser backends exported fixed startup logs, traces, and metrics with linked runtime identities")
}
