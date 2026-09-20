package acceptance

import (
	"bytes"
	"encoding/hex"
	"sync"
	"testing"

	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

const deployedSignalPendingLimit = 256

type deployedSignalInstance struct {
	service                        string
	trace, log, metric, correlated bool
	pending                        map[string]uint8
}

type deployedSignalEvidence struct {
	private   []string
	instances map[string]*deployedSignalInstance
	failure   string
	batches   uint64
}

func (e *deployedSignalEvidence) reject(reason string) {
	if e.failure == "" {
		e.failure = reason
	}
}

func (e *deployedSignalEvidence) instance(attrs []*commonpb.KeyValue) *deployedSignalInstance {
	if len(attrs) != 2 {
		e.reject("invalid resource attributes")
		return nil
	}
	var service, id string
	for _, attr := range attrs {
		if attr == nil {
			e.reject("invalid resource attribute")
			return nil
		}
		value, ok := attr.GetValue().GetValue().(*commonpb.AnyValue_StringValue)
		if !ok {
			e.reject("invalid resource value")
			return nil
		}
		switch attr.GetKey() {
		case "service.name":
			service = value.StringValue
		case "service.instance.id":
			id = value.StringValue
		default:
			e.reject("unexpected resource attribute")
			return nil
		}
	}
	if (service != "hypershell-deployment" && service != "hypershell-gateway-identity") || !telemetryInstancePattern.MatchString(id) {
		e.reject("invalid service instance")
		return nil
	}
	if e.instances == nil {
		e.instances = map[string]*deployedSignalInstance{}
	}
	if existing := e.instances[id]; existing != nil {
		if existing.service != service {
			e.reject("instance crossed services")
			return nil
		}
		return existing
	}
	count := 0
	for _, item := range e.instances {
		if item.service == service {
			count++
		}
	}
	if count >= 2 {
		e.reject("too many service instances")
		return nil
	}
	item := &deployedSignalInstance{service: service, pending: map[string]uint8{}}
	e.instances[id] = item
	return item
}

func (e *deployedSignalEvidence) pair(s *deployedSignalInstance, trace, span []byte, kind uint8) {
	if len(trace) != 16 || len(span) != 8 || bytes.Equal(trace, make([]byte, 16)) || bytes.Equal(span, make([]byte, 8)) {
		e.reject("invalid correlation identity")
		return
	}
	if s.correlated {
		return
	}
	key := hex.EncodeToString(trace) + hex.EncodeToString(span)
	prior, exists := s.pending[key]
	if !exists && len(s.pending) >= deployedSignalPendingLimit {
		e.reject("correlation limit exceeded")
		return
	}
	if prior|kind == 3 {
		s.correlated = true
		s.pending = nil
		return
	}
	s.pending[key] = prior | kind
}

// Consume every batch during the workflow. Retain only bounded correlation IDs
// and fixed status fields. Check private values before any payload is released.
func (e *deployedSignalEvidence) collect(message proto.Message) {
	e.batches++
	if e.failure != "" {
		return
	}
	if message == nil || !message.ProtoReflect().IsValid() || proto.Size(message) > 1<<20 {
		e.reject("invalid signal batch")
		return
	}
	raw, err := proto.Marshal(message)
	if err != nil {
		e.reject("cannot inspect signal batch")
		return
	}
	for _, private := range e.private {
		if bytes.Contains(raw, []byte(private)) {
			e.reject("telemetry exposed private data")
			return
		}
	}
	switch batch := message.(type) {
	case *tracecollector.ExportTraceServiceRequest:
		for _, resource := range batch.GetResourceSpans() {
			s := e.instance(resource.GetResource().GetAttributes())
			if s == nil {
				return
			}
			worker := s.service == "hypershell-gateway-identity"
			for _, scope := range resource.GetScopeSpans() {
				for _, span := range scope.GetSpans() {
					if (!worker && span.GetName() != "" && span.GetKind() == tracepb.Span_SPAN_KIND_SERVER) || (worker && span.GetName() == "controller.reconcile" && span.GetKind() == tracepb.Span_SPAN_KIND_INTERNAL) {
						s.trace = true
						e.pair(s, span.GetTraceId(), span.GetSpanId(), 1)
					}
				}
			}
		}
	case *logcollector.ExportLogsServiceRequest:
		for _, resource := range batch.GetResourceLogs() {
			s := e.instance(resource.GetResource().GetAttributes())
			if s == nil {
				return
			}
			worker := s.service == "hypershell-gateway-identity"
			for _, scope := range resource.GetScopeLogs() {
				for _, record := range scope.GetLogRecords() {
					if (!worker && record.GetEventName() == "http.server.request.completed") || (worker && record.GetEventName() == "controller.work.completed" && deployedSignalOperation(record.GetAttributes()) == "reconcile") {
						s.log = true
						e.pair(s, record.GetTraceId(), record.GetSpanId(), 2)
					}
				}
			}
		}
	case *metriccollector.ExportMetricsServiceRequest:
		for _, resource := range batch.GetResourceMetrics() {
			s := e.instance(resource.GetResource().GetAttributes())
			if s == nil {
				return
			}
			worker := s.service == "hypershell-gateway-identity"
			for _, scope := range resource.GetScopeMetrics() {
				for _, metric := range scope.GetMetrics() {
					if (!worker && metric.GetName() == "http.server.request.duration") || (worker && metric.GetName() == "stego.controller.work.duration") {
						for _, point := range metric.GetHistogram().GetDataPoints() {
							if point.GetCount() > 0 {
								s.metric = true
							}
						}
					}
				}
			}
		}
	default:
		e.reject("unexpected signal batch")
	}
}

func deployedSignalOperation(attrs []*commonpb.KeyValue) string {
	for _, attr := range attrs {
		if attr != nil && attr.GetKey() == "operation" {
			return attr.GetValue().GetStringValue()
		}
	}
	return ""
}

func (e *deployedSignalEvidence) complete() bool {
	if e.failure != "" || len(e.instances) != 4 {
		return false
	}
	counts := map[string]int{}
	for _, s := range e.instances {
		counts[s.service]++
		if !s.trace || !s.log || !s.metric || !s.correlated {
			return false
		}
	}
	return counts["hypershell-deployment"] == 2 && counts["hypershell-gateway-identity"] == 2
}

type deployedSignalReader struct {
	evidence       deployedSignalEvidence
	stopOnce       sync.Once
	stopping, done chan struct{}
}

// Start after the Gateway ID is known, before provider work and Pod replacement.
// The channels retain the startup batches until this reader starts.
func startDeployedSignalReader(signals *httpDiagnosticCollector, private []string) *deployedSignalReader {
	r := &deployedSignalReader{evidence: deployedSignalEvidence{private: append([]string(nil), private...)}, stopping: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(r.done)
		for {
			select {
			case <-r.stopping:
				// Writers have stopped on the normal path. On failure, cleanup
				// still reads at most the current bounded channel contents.
				for n := len(signals.traces.received); n > 0; n-- {
					r.evidence.collect(<-signals.traces.received)
				}
				for n := len(signals.logs.received); n > 0; n-- {
					r.evidence.collect(<-signals.logs.received)
				}
				for n := len(signals.metrics.received); n > 0; n-- {
					r.evidence.collect(<-signals.metrics.received)
				}
				return
			default:
			}
			select {
			case value := <-signals.traces.received:
				r.evidence.collect(value)
			case value := <-signals.logs.received:
				r.evidence.collect(value)
			case value := <-signals.metrics.received:
				r.evidence.collect(value)
			case <-r.stopping:
			}
		}

	}()
	return r
}

func (r *deployedSignalReader) stop() {
	r.stopOnce.Do(func() { close(r.stopping) })
	<-r.done
}

func (r *deployedSignalReader) check(t *testing.T) {
	t.Helper()
	r.stop()
	if !r.evidence.complete() {
		counts := map[string][4]int{}
		for _, s := range r.evidence.instances {
			values := counts[s.service]
			for i, present := range []bool{s.trace, s.log, s.metric, s.correlated} {
				if present {
					values[i]++
				}
			}
			counts[s.service] = values
		}
		t.Fatal("both deployed instances require correlated spans, logs, and metrics", counts, r.evidence.failure)
	}
	t.Log("Both API and identity-worker instances passed private-data checks and exported metrics and correlated logs and traces", r.evidence.batches)
}
