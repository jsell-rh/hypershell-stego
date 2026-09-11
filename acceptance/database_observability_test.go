package acceptance

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestGatewayDatabaseSignalsAcrossRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, auth := issuer(t)
	signals, exports := newHTTPDiagnosticCollector(t)
	settings := append(append([]string{}, auth...), exports...)
	settings = append(settings, "OTEL_SERVICE_NAME=hypershell-database-api")
	cert := identity(t, "localhost")
	directory := filepath.Dir(cert.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	binary := buildApplication(t)
	owner := token(t, key, "alice", "gateway:creator")
	other := token(t, key, "mallory")
	private := []string{owner, other, f.dsn, "private-database-gateway", "private-rejected-gateway", "reject_private_database_event"}
	var gateway httpapi.Gateway
	gatewayGrants := func() int {
		t.Helper()
		var total int
		if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE scope='gateway'").Scan(&total); err != nil {
			t.Fatal(err)
		}
		return total
	}
	previous := ""
	sequence := 0
	for phase := 0; phase < 2; phase++ {
		stop, address, rpcAddress, _, output := startBothWithLogs(t, binary, f.dsn, config, settings...)
		wanted := map[string]string{}
		call := func(method, path, bearer, body string, status int, outcome string) []byte {
			t.Helper()
			sequence++
			id := fmt.Sprintf("%032x", sequence)
			request, err := http.NewRequest(method, address+"/api/hypershell/v1/gateways"+path, strings.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+bearer)
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set("traceparent", "00-"+id+"-2222222222222222-01")
			response, err := (&http.Client{Timeout: 6 * time.Second}).Do(request)
			if err != nil {
				t.Fatal(err)
			}
			data, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
			response.Body.Close()
			if err != nil || response.StatusCode != status {
				t.Fatal("database workflow response", method, response.StatusCode, status, err)
			}
			wanted[id] = outcome
			return data
		}
		if phase == 0 {
			input, err := json.Marshal(f.request("private-database-gateway"))
			if err != nil {
				t.Fatal(err)
			}
			body := string(input)
			if json.Unmarshal(call("POST", "", owner, body, 201, "success"), &gateway) != nil || gateway.ID == "" {
				t.Fatal("Gateway creation failed")
			}
			private = append(private, gateway.ID)
			var ownerGrants int
			if err := f.db.QueryRow(`SELECT count(*) FROM role_bindings b JOIN roles r ON r.id=b.role_id JOIN users u ON u.id=b.user_id WHERE b.gateway_id=$1 AND b.scope='gateway' AND r.name='gateway:owner' AND u.username='alice'`, gateway.ID).Scan(&ownerGrants); err != nil || ownerGrants != 1 {
				t.Fatal("Gateway owner grant differs", ownerGrants, err)
			}

			if readEvent(t, consumer, gateway.ID) == "" {
				t.Fatal("Gateway event missing")
			}
			awaitQueueEmpty(t, f)
			if count(t, f.db, "gateways") != 1 || gatewayGrants() != 1 {
				t.Fatal("Gateway owner grant missing")
			}
			if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_private_database_event CHECK(kind <> 'gateway.created') NOT VALID"); err != nil {
				t.Fatal(err)
			}
			call("POST", "", owner, strings.ReplaceAll(body, "private-database-gateway", "private-rejected-gateway"), 500, "failure")
			if count(t, f.db, "gateways") != 1 || gatewayGrants() != 1 || count(t, f.db, "stego_outbox.messages") != 0 {
				t.Fatal("failed event did not roll back Gateway and grant")
			}
			if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_private_database_event"); err != nil {
				t.Fatal(err)
			}
			call("GET", "/"+gateway.ID, other, "", 404, "success")
			var page struct {
				Total int
				Items []httpapi.Gateway
			}
			if json.Unmarshal(call("GET", "", other, "", 200, "success"), &page) != nil || page.Total != 0 || len(page.Items) != 0 {
				t.Fatal("list exposed the Gateway")
			}
		} else {
			var got httpapi.Gateway
			if json.Unmarshal(call("GET", "/"+gateway.ID, owner, "", 200, "success"), &got) != nil || got.ID != gateway.ID || got.Name != gateway.Name {
				t.Fatal("restart lost the Gateway")
			}
			client, connection := grpcClient(t, rpcAddress, cert)
			for _, bearer := range []string{owner, other} {
				sequence++
				traceID := fmt.Sprintf("%032x", sequence)
				ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
				ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer, "traceparent", "00-"+traceID+"-2222222222222222-01"))
				got, err := client.GetGateway(ctx, &pb.GetGatewayRequest{Id: gateway.ID})
				cancel()
				if bearer == owner {
					if err != nil || got.GetGateway().GetMetadata().GetId() != gateway.ID || got.GetGateway().GetName() != gateway.Name {
						t.Fatal("gRPC restart read differs", err)
					}
				} else if status.Code(err) != codes.NotFound {
					t.Fatal("gRPC access was not denied", err)
				}
				wanted[traceID] = "success"
			}
			connection.Close()
		}
		stop()
		instance := checkGatewayDatabaseSignals(t, signals, output(), private, wanted)
		if instance == previous {
			t.Fatal("database signals reused a stopped runtime identity")
		}
		previous = instance
	}
}

func checkGatewayDatabaseSignals(t *testing.T, c *httpDiagnosticCollector, local string, private []string, wanted map[string]string) string {
	t.Helper()
	check := func(data []byte) {
		t.Helper()
		for _, secret := range private {
			if strings.Contains(string(data), secret) {
				t.Fatal("database signals exposed private data")
			}
		}
	}
	check([]byte(local))
	spans := map[string]*tracepb.Span{}
	database := map[string]*tracepb.Span{}
	logs := map[string]*logpb.LogRecord{}
	localIDs := map[string]bool{}
	instance := ""
	for _, line := range strings.Split(local, "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) == nil && record["event.name"] == "db.client.operation.completed" {
			id, _ := record["span_id"].(string)
			localIDs[id] = true
		}
	}
	for len(c.traces.received) > 0 {
		batch := <-c.traces.received
		data, _ := proto.Marshal(batch)
		check(data)
		for _, resource := range batch.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					id := hex.EncodeToString(span.SpanId)
					spans[id] = span
					if scope.Scope.Name == "stego/database" {
						database[id] = span
						instance = telemetryInstance(t, resource.Resource.Attributes, "hypershell-database-api")
					}
				}
			}
		}
	}
	for len(c.logs.received) > 0 {
		batch := <-c.logs.received
		data, _ := proto.Marshal(batch)
		check(data)
		for _, resource := range batch.ResourceLogs {
			for _, scope := range resource.ScopeLogs {
				if scope.Scope.Name == "stego/database" {
					if telemetryInstance(t, resource.Resource.Attributes, "hypershell-database-api") != instance {
						t.Fatal("database logs use a different instance")
					}
					for _, record := range scope.LogRecords {
						logs[hex.EncodeToString(record.SpanId)] = record
					}
				}
			}
		}
	}
	measured := map[string]bool{}
	for len(c.metrics.received) > 0 {
		batch := <-c.metrics.received
		data, _ := proto.Marshal(batch)
		check(data)
		for _, resource := range batch.ResourceMetrics {
			for _, scope := range resource.ScopeMetrics {
				if scope.Scope.Name == "stego/database" {
					if telemetryInstance(t, resource.Resource.Attributes, "hypershell-database-api") != instance {
						t.Fatal("database metrics use a different instance")
					}
					for _, metric := range scope.Metrics {
						if metric.Name == "db.client.operation.duration" {
							for _, point := range metric.GetHistogram().DataPoints {
								if point.Count > 0 {
									measured[signalAttribute(point.Attributes, "outcome").GetStringValue()] = true
								}
							}
						}
					}
				}
			}
		}
	}
	for traceID, outcome := range wanted {
		found := false
		for id, span := range database {
			if hex.EncodeToString(span.TraceId) != traceID || signalAttribute(span.Attributes, "outcome").GetStringValue() != outcome {
				continue
			}
			parent := spans[hex.EncodeToString(span.ParentSpanId)]
			record := logs[id]
			if span.Kind != tracepb.Span_SPAN_KIND_CLIENT || parent == nil || parent.Kind != tracepb.Span_SPAN_KIND_SERVER || !bytes.Equal(parent.TraceId, span.TraceId) || record == nil || record.EventName != "db.client.operation.completed" || !bytes.Equal(record.TraceId, span.TraceId) || !localIDs[id] || !measured[outcome] {
				continue
			}
			if outcome == "failure" && span.GetStatus().GetCode() != tracepb.Status_STATUS_CODE_ERROR {
				t.Fatal("failed database call has no error status")
			}
			if signalAttribute(record.Attributes, "outcome").GetStringValue() != outcome || signalAttribute(span.Attributes, "db.system.name").GetStringValue() != "postgresql" {
				t.Fatal("database log and span fields differ")
			}
			found = true
		}
		if !found {
			t.Fatal("Gateway call has no correlated database log, metric, and span", traceID, outcome)
		}
	}
	if instance == "" {
		t.Fatal("database runtime identity missing")
	}
	return instance
}
