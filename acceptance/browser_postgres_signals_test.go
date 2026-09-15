package acceptance

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

type postgresSignalPair struct{ log, span, parent string }
type postgresSignalState struct {
	pending             map[string]*postgresSignalPair
	order, parents      []string
	correlated, metrics map[string]bool
	invalid             bool
}

func postgresSignalKey(attrs []*commonpb.KeyValue, log bool) (string, bool) {
	allowed := map[string]bool{"db.system.name": true, "operation": true, "outcome": true, "error.type": true}
	if log {
		allowed["duration_seconds"] = true
	}
	seen := map[string]bool{}
	for _, a := range attrs {
		if !allowed[a.Key] || seen[a.Key] {
			return "", false
		}
		seen[a.Key] = true
	}
	if signalAttribute(attrs, "db.system.name").GetStringValue() != "postgresql" {
		return "", false
	}
	operation := signalAttribute(attrs, "operation").GetStringValue()
	switch operation {
	case "ensure", "delete", "read", "server-identity", "quarantine":
	default:
		return "", false
	}
	outcome := signalAttribute(attrs, "outcome").GetStringValue()
	switch outcome {
	case "success", "failure", "busy", "canceled", "deadline", "aborted":
	default:
		return "", false
	}
	failed := outcome != "success" && outcome != "canceled"
	if failed != seen["error.type"] || failed && signalAttribute(attrs, "error.type").GetStringValue() != outcome {
		return "", false
	}
	if log {
		duration := signalAttribute(attrs, "duration_seconds").GetDoubleValue()
		if !seen["duration_seconds"] || duration < 0 || math.IsNaN(duration) || math.IsInf(duration, 0) {
			return "", false
		}
	}
	return operation + "/" + outcome, true
}
func signalPairID(trace, span []byte) string {
	if len(trace) != 16 || len(span) != 8 {
		return ""
	}
	return hex.EncodeToString(trace) + "/" + hex.EncodeToString(span)
}
func (s *postgresSignalState) pair(id string) *postgresSignalPair {
	if s.pending == nil {
		s.pending = map[string]*postgresSignalPair{}
	}
	if id == "" {
		s.invalid = true
		return &postgresSignalPair{}
	}
	if s.pending[id] == nil {
		if len(s.order) == 256 {
			delete(s.pending, s.order[0])
			s.order = s.order[1:]
		}
		s.order = append(s.order, id)
		s.pending[id] = &postgresSignalPair{}
	}
	return s.pending[id]
}
func (s *postgresSignalState) match() {
	if s.correlated == nil {
		s.correlated = map[string]bool{}
	}
	for _, pair := range s.pending {
		if pair.log == "" || pair.log != pair.span {
			continue
		}
		for _, parent := range s.parents {
			if parent == pair.parent {
				s.correlated[pair.log] = true
				break
			}
		}
	}
}
func (s *postgresSignalState) log(record *logpb.LogRecord) {
	key, valid := postgresSignalKey(record.Attributes, true)
	if !valid || record.EventName != "postgres.database.completed" || record.Body.GetStringValue() != "PostgreSQL database operation completed" {
		s.invalid = true
		return
	}
	s.pair(signalPairID(record.TraceId, record.SpanId)).log = key
	s.match()
}
func (s *postgresSignalState) span(scope string, span *tracepb.Span) {
	if scope == "stego/controller" {
		id := signalPairID(span.TraceId, span.SpanId)
		if len(s.parents) == 128 {
			s.parents = s.parents[1:]
		}
		s.parents = append(s.parents, id)
		s.match()
		return
	}
	if scope != "stego/postgres-client" {
		return
	}
	key, valid := postgresSignalKey(span.Attributes, false)
	operation := signalAttribute(span.Attributes, "operation").GetStringValue()
	if !valid || span.Kind != tracepb.Span_SPAN_KIND_CLIENT || span.Name != "postgres.database."+operation {
		s.invalid = true
		return
	}
	p := s.pair(signalPairID(span.TraceId, span.SpanId))
	p.span = key
	p.parent = signalPairID(span.TraceId, span.ParentSpanId)
	s.match()
}
func (s *postgresSignalState) metric(metric *metricpb.Metric) {
	if metric.Name == "stego.postgres.database.active" {
		sum := metric.GetSum()
		if sum == nil || sum.IsMonotonic || metric.Unit != "{operation}" {
			s.invalid = true
		}
		return
	}
	if metric.Name != "stego.postgres.database.duration" {
		s.invalid = true
		return
	}
	histogram := metric.GetHistogram()
	if histogram == nil || metric.Unit != "s" || histogram.AggregationTemporality != metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
		s.invalid = true
		return
	}
	if s.metrics == nil {
		s.metrics = map[string]bool{}
	}
	for _, point := range histogram.DataPoints {
		key, valid := postgresSignalKey(point.Attributes, false)
		if !valid || point.Count == 0 || point.GetSum() < 0 || math.IsNaN(point.GetSum()) || math.IsInf(point.GetSum(), 0) {
			s.invalid = true
			continue
		}
		s.metrics[key] = true
	}
}
func (w *workerSignalEvidence) checkPostgres(t *testing.T) {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for {
		w.Lock()
		states := w.instances["hypershell-gateway-workload"]
		ready := len(states) == 2 && !w.invalid
		combined := map[string]bool{}
		evidence := map[string]map[string]bool{}
		invalid := w.invalid
		for id, state := range states {
			s := &state.postgres
			invalid = invalid || s.invalid || !telemetryInstancePattern.MatchString(id)
			ready = ready && s.correlated["ensure/success"] && s.metrics["ensure/success"]
			evidence[id] = map[string]bool{}
			for key, yes := range s.correlated {
				if yes && s.metrics[key] {
					combined[key] = true
					evidence[id][key] = true
				}
			}
		}
		ready = ready && combined["delete/failure"] && combined["delete/success"]
		w.Unlock()
		if invalid {
			t.Fatal("PostgreSQL worker signals have invalid fields or identity")
		}
		if ready {
			if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
				body, _ := json.MarshalIndent(evidence, "", "  ")
				if err := os.WriteFile(filepath.Join(directory, "postgres-signals.json"), append(body, '\n'), 0600); err != nil {
					t.Fatal(err)
				}
			}
			t.Log("Gateway SQL operations exported correlated worker logs, traces, and metrics across restart, cleanup denial, and recovery")
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("missing PostgreSQL worker signals across restart, cleanup denial, or recovery")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestPostgresSignalEvidenceRequiresControllerAndLog(t *testing.T) {
	attrs := []*commonpb.KeyValue{}
	for _, item := range [][2]string{{"db.system.name", "postgresql"}, {"operation", "ensure"}, {"outcome", "success"}} {
		attrs = append(attrs, &commonpb.KeyValue{Key: item[0], Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: item[1]}}})
	}
	trace, child, parent := make([]byte, 16), make([]byte, 8), make([]byte, 8)
	trace[0] = 1
	child[0] = 2
	parent[0] = 3
	span := &tracepb.Span{Name: "postgres.database.ensure", Kind: tracepb.Span_SPAN_KIND_CLIENT, TraceId: trace, SpanId: child, ParentSpanId: parent, Attributes: attrs}
	record := &logpb.LogRecord{EventName: "postgres.database.completed", Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "PostgreSQL database operation completed"}}, TraceId: trace, SpanId: child, Attributes: append(append([]*commonpb.KeyValue{}, attrs...), &commonpb.KeyValue{Key: "duration_seconds", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 0.1}}})}
	s := postgresSignalState{}
	s.span("stego/postgres-client", span)
	s.log(record)
	if s.correlated["ensure/success"] {
		t.Fatal("SQL pair passed without a controller parent")
	}
	s.span("stego/controller", &tracepb.Span{TraceId: trace, SpanId: parent})
	if !s.correlated["ensure/success"] || s.invalid {
		t.Fatal("complete controller correlation was not retained")
	}
	other := postgresSignalState{}
	other.span("stego/controller", &tracepb.Span{TraceId: trace, SpanId: parent})
	other.span("stego/postgres-client", span)
	if other.correlated["ensure/success"] {
		t.Fatal("span passed without a log")
	}
	record.Attributes = append(record.Attributes, &commonpb.KeyValue{Key: "db.query.text", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "private query"}}})
	other.log(record)
	if !other.invalid {
		t.Fatal("undeclared SQL field was accepted")
	}
}
