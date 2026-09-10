package acceptance

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestServiceAccountSweepTelemetryAcrossRestart(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	_, gateway := accountService(t, f, provider)
	key, settings := issuer(t)
	rpcSettings, _ := startAccountProvisioner(t, provider, key, settings)
	signals, exportSettings := newHTTPDiagnosticCollector(t)
	settings = append(settings, rpcSettings...)
	settings = append(settings, exportSettings...)
	settings = append(settings, "OTEL_SERVICE_NAME=hypershell-account-recovery")
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	binary := buildApplication(t)
	stop, address, _, _, output := startBothWithLogs(t, binary, f.dsn, config, settings...)
	owner := token(t, key, "alice")
	path := "/api/hypershell/v1/gateways/" + gateway.ID + "/service_accounts"
	code, data := requestJSON(t, "POST", address+path, owner, []byte(`{"name":"private-sweep-account"}`))
	var created struct {
		ID         string `json:"id"`
		Credential struct {
			Secret string `json:"client_secret"`
		} `json:"credential"`
	}
	if code != 201 || json.Unmarshal(data, &created) != nil || created.ID == "" || created.Credential.Secret == "" {
		t.Fatal("account creation failed", code)
	}
	if readEvent(t, consumer, gateway.ID) == "" {
		t.Fatal("Gateway event was not delivered")
	}
	awaitQueueEmpty(t, f)
	provider.mu.Lock()
	provider.failChange = true
	provider.mu.Unlock()
	if code, _ := requestJSON(t, "POST", address+path+"/"+created.ID+"/revoke", owner, nil); code != 202 {
		t.Fatal("account revocation was not retained", code)
	}
	deadline := time.Now().Add(12 * time.Second)
	for !controllerRetryLogged(output()) {
		if time.Now().After(deadline) {
			t.Fatal("account recovery has no shared retry telemetry")
		}
		time.Sleep(50 * time.Millisecond)
	}
	stop()
	private := []string{gateway.ID, created.ID, created.Credential.Secret, owner, "private-sweep-account", "private delete failure"}
	first := checkSweepTelemetry(t, signals, output(), "failure", true, private)
	var state string
	if err := f.db.QueryRow("SELECT status FROM service_accounts WHERE id=$1", created.ID).Scan(&state); err != nil || state != "revoking" {
		t.Fatal("restart did not retain pending revocation", state, err)
	}
	provider.mu.Lock()
	provider.failChange = false
	provider.mu.Unlock()
	stop, address, _, _, output = startBothWithLogs(t, binary, f.dsn, config, settings...)
	deadline = time.Now().Add(12 * time.Second)
	for {
		if err := f.db.QueryRow("SELECT status FROM service_accounts WHERE id=$1", created.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "revoked" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("account recovery did not finish after restart")
		}
		time.Sleep(50 * time.Millisecond)
	}
	provider.mu.Lock()
	_, exists := provider.clients[created.ID]
	provider.mu.Unlock()
	if exists {
		t.Fatal("recovery retained the provider identity")
	}
	if code, data := requestJSON(t, "GET", address+path+"/"+created.ID, owner, nil); code != 200 || bytes.Contains(data, []byte(created.Credential.Secret)) {
		t.Fatal("owner read failed or exposed the credential", code)
	}
	if code, _ := requestJSON(t, "GET", address+path+"/"+created.ID, token(t, key, "mallory"), nil); code != 404 {
		t.Fatal("account read bypassed access rules", code)
	}
	stop()
	second := checkSweepTelemetry(t, signals, output(), "success", false, private)
	if first == second {
		t.Fatal("restart retained the old telemetry instance")
	}
}

func checkSweepTelemetry(t *testing.T, c *httpDiagnosticCollector, local, outcome string, retry bool, private []string) string {
	t.Helper()
	for _, value := range private {
		if strings.Contains(local, value) {
			t.Fatal("local recovery telemetry exposed private data")
		}
	}
	instance := ""
	for _, line := range strings.Split(local, "\n") {
		var record struct {
			Event     string `json:"event.name"`
			Operation string `json:"operation"`
			Outcome   string `json:"outcome"`
			Retry     bool   `json:"retry"`
			Instance  string `json:"service.instance.id"`
		}
		if json.Unmarshal([]byte(line), &record) == nil && record.Event == "controller.work.completed" && record.Operation == "reconcile" && record.Outcome == outcome && record.Retry == retry {
			instance = record.Instance
		}
	}
	if instance == "" {
		t.Fatal("missing local account recovery outcome", outcome)
	}
	checkPrivate := func(message proto.Message) {
		data, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range private {
			if bytes.Contains(data, []byte(value)) {
				t.Fatal("exported recovery telemetry exposed private data")
			}
		}
	}
	matches := func(attrs []*commonpb.KeyValue) bool {
		return signalAttribute(attrs, "operation").GetStringValue() == "reconcile" && signalAttribute(attrs, "outcome").GetStringValue() == outcome && signalAttribute(attrs, "retry").GetBoolValue() == retry
	}
	spans := map[string]*tracepb.Span{}
	clientSpans := map[string]*tracepb.Span{}
	clientRecords := map[string]*logpb.LogRecord{}
	clientInstances := map[string]string{}
	clientMeasured := false
	clientStatus := "OK"
	if retry {
		clientStatus = "UNAVAILABLE"
	}
	records := map[string]*logpb.LogRecord{}
	measured := false
	scanned := false
	for len(c.traces.received) > 0 {
		batch := <-c.traces.received
		checkPrivate(batch)
		for _, resource := range batch.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				if scope.Scope.Name == "stego/grpc-client" {
					for _, span := range scope.Spans {
						id := hex.EncodeToString(span.SpanId)
						clientSpans[id] = span
						clientInstances[id] = telemetryInstance(t, resource.Resource.Attributes, "hypershell-account-recovery")
					}
				}
				if scope.Scope.Name != "stego/controller" {
					continue
				}
				if telemetryInstance(t, resource.Resource.Attributes, "hypershell-account-recovery") != instance {
					t.Fatal("controller trace instance changed")
				}
				for _, span := range scope.Spans {
					if span.Name == "controller.scan" {
						scanned = true
					}
					if span.Name == "controller.reconcile" && matches(span.Attributes) {
						spans[hex.EncodeToString(span.SpanId)] = span
					}
				}
			}
		}
	}
	for len(c.logs.received) > 0 {
		batch := <-c.logs.received
		checkPrivate(batch)
		for _, resource := range batch.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				if scope.Scope.Name == "stego/grpc-client" && telemetryInstance(t, resource.Resource.Attributes, "hypershell-account-recovery") == instance {
					for _, record := range scope.LogRecords {
						if record.EventName == "rpc.client.call.completed" {
							clientRecords[hex.EncodeToString(record.SpanId)] = record
						}
					}
				}
				if scope.Scope.Name != "stego/controller" {
					continue
				}
				if telemetryInstance(t, resource.Resource.Attributes, "hypershell-account-recovery") != instance {
					t.Fatal("controller log instance changed")
				}
				for _, record := range scope.LogRecords {
					if record.EventName == "controller.work.completed" && matches(record.Attributes) {
						records[hex.EncodeToString(record.SpanId)] = record
					}
				}
			}
		}
	}
	for len(c.metrics.received) > 0 {
		batch := <-c.metrics.received
		checkPrivate(batch)
		for _, resource := range batch.ResourceMetrics {
			for _, scope := range resource.ScopeMetrics {
				if scope.Scope.Name == "stego/grpc-client" && telemetryInstance(t, resource.Resource.Attributes, "hypershell-account-recovery") == instance {
					for _, metric := range scope.Metrics {
						if metric.Name == "rpc.client.call.duration" {
							for _, point := range metric.GetHistogram().DataPoints {
								if point.Count > 0 && signalAttribute(point.Attributes, "rpc.response.status_code").GetStringValue() == clientStatus {
									clientMeasured = true
								}
							}
						}
					}
				}
				if scope.Scope.Name != "stego/controller" {
					continue
				}
				if telemetryInstance(t, resource.Resource.Attributes, "hypershell-account-recovery") != instance {
					t.Fatal("controller metric instance changed")
				}
				for _, metric := range scope.Metrics {
					if metric.Name == "stego.controller.work.duration" {
						for _, point := range metric.GetHistogram().DataPoints {
							if point.Count > 0 && matches(point.Attributes) {
								measured = true
							}
						}
					}
				}
			}
		}
	}
	correlated := false
	for id, record := range records {
		if span := spans[id]; span != nil && bytes.Equal(span.TraceId, record.TraceId) {
			correlated = true
		}
	}
	outbound := false
	for id, span := range clientSpans {
		parent := spans[hex.EncodeToString(span.ParentSpanId)]
		record := clientRecords[id]
		if parent != nil && record != nil && clientInstances[id] == instance && bytes.Equal(parent.TraceId, span.TraceId) && bytes.Equal(span.TraceId, record.TraceId) && signalAttribute(span.Attributes, "rpc.response.status_code").GetStringValue() == clientStatus && signalAttribute(record.Attributes, "rpc.response.status_code").GetStringValue() == clientStatus {
			outbound = true
		}
	}
	if !clientMeasured {
		t.Fatal("recovery has no RPC client duration metric", clientStatus)
	}
	if !outbound {
		t.Fatal("recovery has no child RPC client span", outcome)
	}
	if !scanned || !measured || !correlated {
		t.Fatal("incomplete account recovery signals", outcome, scanned, measured, correlated)
	}
	return instance
}
