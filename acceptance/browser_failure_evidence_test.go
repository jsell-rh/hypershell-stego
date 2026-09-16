package acceptance

import (
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

const accountProvisionMethod = "hypershell.provisioner.v1.OpenShellGatewayServiceAccountProvisionerService/Provision"

type accountProvisionEvidence struct {
	Status   string
	Duration time.Duration
	Server   bool
}

// Export only a fixed operation's status, duration, and transport side. Never
// export a span body, error message, arbitrary attribute, or one-time credential.
func accountProvisionSpan(span *tracepb.Span) (accountProvisionEvidence, bool) {
	if span == nil || span.Name != accountProvisionMethod || span.EndTimeUnixNano < span.StartTimeUnixNano || span.EndTimeUnixNano-span.StartTimeUnixNano > uint64(time.Minute) {
		return accountProvisionEvidence{}, false
	}
	if span.Kind != tracepb.Span_SPAN_KIND_SERVER && span.Kind != tracepb.Span_SPAN_KIND_CLIENT {
		return accountProvisionEvidence{}, false
	}
	result := accountProvisionEvidence{Status: "_OTHER", Duration: time.Duration(span.EndTimeUnixNano - span.StartTimeUnixNano), Server: span.Kind == tracepb.Span_SPAN_KIND_SERVER}
	found := false
	for _, entry := range span.Attributes {
		if entry.GetKey() != "rpc.response.status_code" {
			continue
		}
		if found {
			return accountProvisionEvidence{}, false
		}
		found = true
		switch value := entry.GetValue().GetStringValue(); value {
		case "OK", "CANCELLED", "UNKNOWN", "INVALID_ARGUMENT", "DEADLINE_EXCEEDED", "NOT_FOUND", "ALREADY_EXISTS", "PERMISSION_DENIED", "RESOURCE_EXHAUSTED", "FAILED_PRECONDITION", "ABORTED", "OUT_OF_RANGE", "UNIMPLEMENTED", "INTERNAL", "UNAVAILABLE", "DATA_LOSS", "UNAUTHENTICATED":
			result.Status = value
		}
	}
	return result, true
}

func reportAccountProvisioningSpans(t *testing.T, signals *httpDiagnosticCollector) {
	t.Helper()
	if signals == nil || signals.traces == nil {
		return
	}
	count := 0
	// The collector queue is bounded. Do not wait for more records during cleanup.
	for batchCount := 0; batchCount < 64; batchCount++ {
		select {
		case batch := <-signals.traces.received:
			for _, resource := range batch.GetResourceSpans() {
				for _, scope := range resource.GetScopeSpans() {
					for _, span := range scope.GetSpans() {
						if value, ok := accountProvisionSpan(span); ok {
							count++
							t.Logf("Account provisioning trace: server=%t status=%s duration=%s", value.Server, value.Status, value.Duration)
						}
					}
				}
			}
		default:
			t.Logf("Account provisioning trace records retained: %d", count)
			return
		}
	}
	t.Logf("Account provisioning trace records retained: %d", count)
}

func TestAccountProvisionEvidenceExcludesPrivateData(t *testing.T) {
	attribute := func(key, value string) *commonpb.KeyValue {
		return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
	}
	span := &tracepb.Span{Name: accountProvisionMethod, Kind: tracepb.Span_SPAN_KIND_SERVER, StartTimeUnixNano: 1, EndTimeUnixNano: uint64(time.Second) + 1, Status: &tracepb.Status{Message: "private-error"}, Attributes: []*commonpb.KeyValue{attribute("rpc.response.status_code", "INTERNAL"), attribute("private-key", "private-secret")}}
	got, ok := accountProvisionSpan(span)
	if !ok || got != (accountProvisionEvidence{Status: "INTERNAL", Duration: time.Second, Server: true}) {
		t.Fatal("fixed trace summary differs")
	}
	span.Attributes[0] = attribute("rpc.response.status_code", "private-secret")
	got, ok = accountProvisionSpan(span)
	if !ok || got.Status != "_OTHER" {
		t.Fatal("unknown status entered trace evidence")
	}
	span.Attributes = append(span.Attributes, attribute("rpc.response.status_code", "OK"))
	if _, ok := accountProvisionSpan(span); ok {
		t.Fatal("duplicate status accepted")
	}
	span.Name = "private-method"
	if _, ok := accountProvisionSpan(span); ok {
		t.Fatal("unknown operation accepted")
	}
}
