package acceptance

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestKeycloakHTTPClientTelemetryAcrossRestart(t *testing.T) {
	k := startKeycloak(t)
	f := database(t)
	_, gateway := accountService(t, f, newAccountProvider())
	k.bindGateway(t, "gateway-audience", gateway.ID)
	oidc := fmt.Sprintf(`{"issuer":%q,"client_id":"gateway-audience","audience":"gateway-audience"}`, k.options.ServerURL+"/realms/workflow")
	if _, err := f.db.Exec("UPDATE gateways SET oidc=$1 WHERE id=$2", oidc, gateway.ID); err != nil {
		t.Fatal(err)
	}
	observeGatewayFixture(t, f, gateway.ID)
	key, auth := issuer(t)
	signals, exports := newHTTPDiagnosticCollector(t)
	providerEnv := append(append([]string{}, auth...), exports...)
	providerEnv = append(providerEnv, "OTEL_SERVICE_NAME=hypershell-keycloak-provider")
	providerSettings, stopProvider, providerOutput := startRealProvisionerWithLogs(t, k, key, providerEnv)
	apiEnv := append(append([]string{}, auth...), exports...)
	apiEnv = append(apiEnv, "OTEL_SERVICE_NAME=hypershell-keycloak-api")
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	binary := buildApplication(t)
	stopAPI, address, _, _, apiOutput := startBothWithLogs(t, binary, f.dsn, config, append(apiEnv, providerSettings...)...)
	owner := token(t, key, "alice")
	path := "/api/hypershell/v1/gateways/" + gateway.ID + "/service_accounts"
	code, data := requestJSON(t, "POST", address+path, owner, []byte(`{"name":"private-http-account"}`))
	var created struct {
		ID         string `json:"id"`
		ClientID   string `json:"client_id"`
		Credential struct {
			Secret string `json:"client_secret"`
		} `json:"credential"`
	}
	if code != 201 || json.Unmarshal(data, &created) != nil || created.ID == "" || created.Credential.Secret == "" {
		t.Fatal("real account creation failed", code)
	}
	if response, _ := k.issue(t, created.ClientID, created.Credential.Secret); response.StatusCode != 200 {
		t.Fatal("new credential cannot obtain a token")
	}
	if readEvent(t, consumer, gateway.ID) == "" {
		t.Fatal("Gateway event was not delivered")
	}
	awaitQueueEmpty(t, f)
	if code, _ := requestJSON(t, "GET", address+path+"/"+created.ID, token(t, key, "mallory"), nil); code != 404 {
		t.Fatal("account access was not denied", code)
	}
	stopAPI()
	stopProvider()
	private := []string{gateway.ID, created.ID, created.Credential.Secret, owner, "private-http-account", "acceptance-only-admin-secret"}
	first := checkKeycloakHTTPClientSignals(t, signals, providerOutput()+apiOutput(), private, "Create")
	providerSettings, stopProvider, providerOutput = startRealProvisionerWithLogs(t, k, key, providerEnv)
	stopAPI, address, _, _, apiOutput = startBothWithLogs(t, binary, f.dsn, config, append(apiEnv, providerSettings...)...)
	if code, data := requestJSON(t, "GET", address+path+"/"+created.ID, owner, nil); code != 200 || bytes.Contains(data, []byte(created.Credential.Secret)) {
		t.Fatal("restart read failed or exposed a secret", code)
	}
	if code, _ := requestJSON(t, "POST", address+path+"/"+created.ID+"/revoke", owner, nil); code != 200 && code != 202 {
		t.Fatal("revoke failed", code)
	}
	deadline := time.Now().Add(12 * time.Second)
	for {
		var state string
		if err := f.db.QueryRow("SELECT status FROM service_accounts WHERE id=$1", created.ID).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "revoked" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revoke did not finish")
		}
		time.Sleep(50 * time.Millisecond)
	}
	if response, _ := k.issue(t, created.ClientID, created.Credential.Secret); response.StatusCode == 200 {
		t.Fatal("revoked credential still works")
	}
	stopAPI()
	stopProvider()
	second := checkKeycloakHTTPClientSignals(t, signals, providerOutput()+apiOutput(), private, "Revoke")
	if first == second {
		t.Fatal("provider restart retained the old runtime identity")
	}
}

func checkKeycloakHTTPClientSignals(t *testing.T, c *httpDiagnosticCollector, local string, private []string, phase string) string {
	t.Helper()
	for _, secret := range private {
		if strings.Contains(local, secret) {
			t.Fatal("local provider logs exposed private data")
		}
	}
	check := func(m proto.Message) {
		data, _ := proto.Marshal(m)
		for _, secret := range private {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("provider export exposed private data")
			}
		}
	}
	spans := map[string]*tracepb.Span{}
	httpSpans := map[string]*tracepb.Span{}
	instances := map[string]string{}
	for len(c.traces.received) > 0 {
		batch := <-c.traces.received
		check(batch)
		for _, resource := range batch.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					id := hex.EncodeToString(span.SpanId)
					spans[id] = span
					if scope.Scope.Name == "stego/http-client" {
						httpSpans[id] = span
						instances[id] = telemetryInstance(t, resource.Resource.Attributes, "hypershell-keycloak-provider")
					}
				}
			}
		}
	}
	logs := map[string]*logpb.LogRecord{}
	for len(c.logs.received) > 0 {
		batch := <-c.logs.received
		check(batch)
		for _, resource := range batch.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				if scope.Scope.Name == "stego/http-client" {
					for _, record := range scope.LogRecords {
						logs[hex.EncodeToString(record.SpanId)] = record
					}
				}
			}
		}
	}
	metricKey := func(instance string, attrs []*commonpb.KeyValue) string {
		return fmt.Sprintf("%s/%s/%d/%s", instance, signalAttribute(attrs, "http.request.method").GetStringValue(), signalAttribute(attrs, "http.response.status_code").GetIntValue(), signalAttribute(attrs, "outcome").GetStringValue())
	}
	measured := map[string]bool{}
	for len(c.metrics.received) > 0 {
		batch := <-c.metrics.received
		check(batch)
		for _, resource := range batch.ResourceMetrics {
			for _, scope := range resource.ScopeMetrics {
				if scope.Scope.Name == "stego/http-client" {
					for _, m := range scope.Metrics {
						if m.Name == "http.client.request.duration" {
							for _, point := range m.GetHistogram().DataPoints {
								if point.Count > 0 {
									measured[metricKey(telemetryInstance(t, resource.Resource.Attributes, "hypershell-keycloak-provider"), point.Attributes)] = true
								}
							}
						}
					}
				}
			}
		}
	}
	if len(httpSpans) == 0 || len(measured) == 0 {
		t.Fatal("real Keycloak workflow has no generated HTTP client signals", phase)
	}
	expectedMethod := "/Provision"
	if phase == "Revoke" {
		expectedMethod = "/Delete"
	}
	for id, span := range httpSpans {
		if !measured[metricKey(instances[id], span.Attributes)] {
			continue
		}
		record := logs[id]
		if record == nil || record.EventName != "http.client.request.completed" || !bytes.Equal(record.TraceId, span.TraceId) {
			continue
		}
		// Follow the actual chain from HTTP client to provider server, API client,
		// and API request or recovery work. All nodes must retain the same trace.
		parent := spans[hex.EncodeToString(span.ParentSpanId)]
		if parent == nil || !strings.HasSuffix(parent.Name, expectedMethod) || parent.Kind != tracepb.Span_SPAN_KIND_SERVER || !bytes.Equal(parent.TraceId, span.TraceId) {
			continue
		}
		caller := spans[hex.EncodeToString(parent.ParentSpanId)]
		if caller == nil || caller.Name != parent.Name || caller.Kind != tracepb.Span_SPAN_KIND_CLIENT || !bytes.Equal(caller.TraceId, span.TraceId) {
			continue
		}
		root := spans[hex.EncodeToString(caller.ParentSpanId)]
		if root == nil || !bytes.Equal(root.TraceId, span.TraceId) {
			continue
		}
		if span.Kind != tracepb.Span_SPAN_KIND_CLIENT || span.EndTimeUnixNano < span.StartTimeUnixNano {
			t.Fatal("invalid provider HTTP span")
		}
		localMatch := false
		for _, line := range strings.Split(local, "\n") {
			var item map[string]any
			if json.Unmarshal([]byte(line), &item) == nil && item["event.name"] == "http.client.request.completed" && item["span_id"] == id && item["service.instance.id"] == instances[id] {
				localMatch = true
			}
		}
		if !localMatch {
			t.Fatal("provider HTTP span has no local completion log")
		}
		return instances[id]
	}
	t.Fatal("Keycloak HTTP call has no complete request trace", phase)
	return ""
}
