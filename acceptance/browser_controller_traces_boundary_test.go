package acceptance

import (
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func controllerTraceFixture(sequence uint64, operation string) (*logpb.LogRecord, *tracepb.Span) {
	trace, span := make([]byte, 16), make([]byte, 8)
	binary.BigEndian.PutUint64(trace[8:], sequence)
	binary.BigEndian.PutUint64(span, sequence)
	attrs := []*commonpb.KeyValue{
		{Key: "operation", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: operation}}},
		{Key: "outcome", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: "success"}}},
		{Key: "retry", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: false}}},
	}
	root := &tracepb.Span{Name: "controller." + operation, Kind: tracepb.Span_SPAN_KIND_INTERNAL, TraceId: trace, SpanId: span, Attributes: attrs}
	record := &logpb.LogRecord{EventName: "controller.work.completed", TraceId: append([]byte{}, trace...), SpanId: append([]byte{}, span...)}
	for _, a := range attrs {
		record.Attributes = append(record.Attributes, proto.Clone(a).(*commonpb.KeyValue))
	}
	record.Attributes = append(record.Attributes, &commonpb.KeyValue{Key: "duration_seconds", Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: 0.1}}})
	return record, root
}

func TestControllerTraceEvidenceRejectsInvalidRoots(t *testing.T) {
	cases := []struct {
		name   string
		change func(*logpb.LogRecord, *tracepb.Span)
	}{
		{"watch parent", func(_ *logpb.LogRecord, s *tracepb.Span) { s.ParentSpanId = []byte{1, 2, 3, 4, 5, 6, 7, 8} }},
		{"zero parent", func(_ *logpb.LogRecord, s *tracepb.Span) { s.ParentSpanId = make([]byte, 8) }},
		{"zero trace", func(_ *logpb.LogRecord, s *tracepb.Span) { s.TraceId = make([]byte, 16) }},
		{"zero span", func(l *logpb.LogRecord, _ *tracepb.Span) { l.SpanId = make([]byte, 8) }},
		{"short trace", func(_ *logpb.LogRecord, s *tracepb.Span) { s.TraceId = []byte{1} }},
		{"short span", func(l *logpb.LogRecord, _ *tracepb.Span) { l.SpanId = []byte{1} }},
		{"link", func(_ *logpb.LogRecord, s *tracepb.Span) { s.Links = []*tracepb.Span_Link{{}} }},
		{"event", func(_ *logpb.LogRecord, s *tracepb.Span) { s.Events = []*tracepb.Span_Event{{}} }},
		{"trace state", func(_ *logpb.LogRecord, s *tracepb.Span) { s.TraceState = "private=value" }},
		{"wrong name", func(_ *logpb.LogRecord, s *tracepb.Span) { s.Name = "controller.watch" }},
		{"wrong kind", func(_ *logpb.LogRecord, s *tracepb.Span) { s.Kind = tracepb.Span_SPAN_KIND_CLIENT }},
		{"unknown operation", func(_ *logpb.LogRecord, s *tracepb.Span) {
			s.Attributes[0].Value.Value = &commonpb.AnyValue_StringValue{StringValue: "private"}
		}},
		{"unknown outcome", func(l *logpb.LogRecord, _ *tracepb.Span) {
			l.Attributes[1].Value.Value = &commonpb.AnyValue_StringValue{StringValue: "private"}
		}},
		{"missing field", func(_ *logpb.LogRecord, s *tracepb.Span) { s.Attributes = s.Attributes[:2] }},
		{"duplicate field", func(l *logpb.LogRecord, _ *tracepb.Span) { l.Attributes = append(l.Attributes, l.Attributes[0]) }},
		{"private field", func(_ *logpb.LogRecord, s *tracepb.Span) {
			s.Attributes = append(s.Attributes, &commonpb.KeyValue{Key: "private", Value: &commonpb.AnyValue{}})
		}},
		{"wrong type", func(l *logpb.LogRecord, _ *tracepb.Span) {
			l.Attributes[2].Value.Value = &commonpb.AnyValue_StringValue{StringValue: "false"}
		}},
		{"retry on success", func(_ *logpb.LogRecord, s *tracepb.Span) {
			s.Attributes[2].Value.Value = &commonpb.AnyValue_BoolValue{BoolValue: true}
		}},
		{"pending scan", func(_ *logpb.LogRecord, s *tracepb.Span) {
			s.Name = "controller.scan"
			s.Attributes[0].Value.Value = &commonpb.AnyValue_StringValue{StringValue: "scan"}
			s.Attributes[1].Value.Value = &commonpb.AnyValue_StringValue{StringValue: "pending"}
		}},
		{"negative duration", func(l *logpb.LogRecord, _ *tracepb.Span) {
			l.Attributes[3].Value.Value = &commonpb.AnyValue_DoubleValue{DoubleValue: -1}
		}},
		{"nan duration", func(l *logpb.LogRecord, _ *tracepb.Span) {
			l.Attributes[3].Value.Value = &commonpb.AnyValue_DoubleValue{DoubleValue: math.NaN()}
		}},
		{"infinite duration", func(l *logpb.LogRecord, _ *tracepb.Span) {
			l.Attributes[3].Value.Value = &commonpb.AnyValue_DoubleValue{DoubleValue: math.Inf(1)}
		}},
		{"pair outcome mismatch", func(l *logpb.LogRecord, _ *tracepb.Span) {
			l.Attributes[1].Value.Value = &commonpb.AnyValue_StringValue{StringValue: "pending"}
		}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			log, span := controllerTraceFixture(1, "reconcile")
			test.change(log, span)
			s := &controllerTraceState{}
			s.log(log)
			s.span(span)
			if !s.invalid {
				t.Fatal("invalid controller evidence was accepted")
			}
		})
	}
}

func TestControllerTraceEvidenceRequiresDistinctPairedWork(t *testing.T) {
	for _, reverse := range []bool{false, true} {
		t.Run(fmt.Sprint(reverse), func(t *testing.T) {
			s := &controllerTraceState{}
			for sequence := uint64(1); sequence <= 2; sequence++ {
				log, span := controllerTraceFixture(sequence, "reconcile")
				if reverse {
					s.span(span)
					s.log(log)
				} else {
					s.log(log)
					s.span(span)
				}
				s.log(log)
				s.span(span)
				if s.invalid || s.counts["reconcile"] != int(sequence) {
					t.Fatal("pair order or duplicate delivery changed the count", s.counts)
				}
			}
			log, span := controllerTraceFixture(3, "reconcile")
			span.TraceId[15] = 2
			log.TraceId[15] = 2
			s.log(log)
			s.span(span)
			if !s.invalid {
				t.Fatal("two work spans reused one trace")
			}
		})
	}
	t.Run("bounded unmatched records", func(t *testing.T) {
		s := &controllerTraceState{}
		for i := uint64(1); i <= controllerTracePairLimit+20; i++ {
			_, span := controllerTraceFixture(i, "cleanup")
			s.span(span)
		}
		if s.invalid || len(s.pairs) != controllerTracePairLimit || len(s.order) != controllerTracePairLimit || len(s.owners) != controllerTracePairLimit || len(s.counts) != 0 {
			t.Fatal("unmatched collection is unbounded or claimed a pair")
		}
		old, _ := controllerTraceFixture(1, "cleanup")
		s.log(old)
		if len(s.counts) != 0 {
			t.Fatal("evicted span was treated as a retained pair")
		}
		latest, _ := controllerTraceFixture(controllerTracePairLimit+20, "cleanup")
		s.log(latest)
		if s.invalid || s.counts["cleanup"] != 1 || len(s.pairs) > controllerTracePairLimit || len(s.owners) > controllerTracePairLimit {
			t.Fatal("bounded pair recovery failed")
		}
	})
}

func controllerTraceServicesFixture(allocation, identity, workload int) *workerSignalEvidence {
	w := &workerSignalEvidence{instances: map[string]map[string]*workerSignalState{}}
	for _, worker := range []struct {
		name  string
		count int
	}{
		{"hypershell-namespace-allocation", allocation},
		{"hypershell-gateway-identity", identity},
		{"hypershell-gateway-workload", workload},
	} {
		w.instances[worker.name] = map[string]*workerSignalState{}
		for i := 1; i <= worker.count; i++ {
			id := fmt.Sprintf("%08x-0000-4000-8000-000000000001", i)
			state := &workerSignalState{}
			w.instances[worker.name][id] = state
			sequence := uint64(0)
			for _, operation := range []string{"reconcile", "scan", "cleanup"} {
				for range 2 {
					sequence++
					log, span := controllerTraceFixture(sequence, operation)
					state.controller.log(log)
					state.controller.span(span)
				}
			}
		}
	}
	return w
}

func TestControllerTraceEvidenceRequiresEachServiceAndInstance(t *testing.T) {
	for _, mode := range []string{"complete", "short old instance", "split operation proof", "missing cleanup", "missing instance", "uncorrelated instance", "invalid root", "wrong identity"} {
		t.Run(mode, func(t *testing.T) {
			w := controllerTraceServicesFixture(3, 2, 2)
			service := w.instances["hypershell-namespace-allocation"]
			id := "00000001-0000-4000-8000-000000000001"
			switch mode {
			case "short old instance":
				for op := range service[id].controller.counts {
					service[id].controller.counts[op] = 1
				}
			case "split operation proof":
				delete(service[id].controller.counts, "cleanup")
				for otherID, state := range service {
					if otherID != id {
						delete(state.controller.counts, "scan")
					}
				}
			case "missing cleanup":
				for _, s := range service {
					delete(s.controller.counts, "cleanup")
				}
			case "missing instance":
				delete(service, id)
			case "uncorrelated instance":
				service[id].controller.counts = nil
			case "invalid root":
				service[id].controller.invalid = true
			case "wrong identity":
				service["private-instance"] = service[id]
				delete(service, id)
			}
			ready, invalid, _ := w.controllerTraceStatus(false)
			wantReady := mode == "complete" || mode == "short old instance"
			wantInvalid := mode == "invalid root" || mode == "wrong identity"
			if ready != wantReady || invalid != wantInvalid {
				t.Fatal("service evidence did not enforce the required scope", ready, invalid)
			}
		})
	}
}

func TestControllerTraceEvidenceRequiresExactCleanupProfile(t *testing.T) {
	for _, profile := range []struct {
		name                           string
		public, endpointChange         bool
		allocation, identity, workload int
	}{
		{"internal", false, false, 3, 2, 2},
		{"public", true, false, 3, 2, 4},
		{"internal endpoint change", false, true, 4, 2, 3},
		{"public endpoint change", true, true, 4, 2, 5},
	} {
		t.Run(profile.name, func(t *testing.T) {
			for _, change := range []struct {
				name                           string
				allocation, identity, workload int
			}{
				{"complete", 0, 0, 0},
				{"missing resumed allocator", -1, 0, 0},
				{"extra allocator", 1, 0, 0},
				{"missing identity", 0, -1, 0},
				{"extra identity", 0, 1, 0},
				{"missing workload", 0, 0, -1},
				{"extra workload", 0, 0, 1},
			} {
				t.Run(change.name, func(t *testing.T) {
					w := controllerTraceServicesFixture(profile.allocation+change.allocation, profile.identity+change.identity, profile.workload+change.workload)
					ready, invalid, evidence := w.controllerTraceStatus(profile.public, profile.endpointChange)
					if ready != (change.name == "complete") || invalid {
						t.Fatal("cleanup evidence did not enforce the exact process counts", ready, invalid, evidence)
					}
				})
			}
		})
	}
}
