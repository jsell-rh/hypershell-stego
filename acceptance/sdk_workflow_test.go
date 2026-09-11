package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/pem"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/jsell-rh/hypershell-stego/out/sdk"
	"github.com/segmentio/ksuid"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

func TestGeneratedGoSDKGatewayWorkflow(t *testing.T) {
	f := database(t)
	_, brokerConfig := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, brokerConfig)
	key, auth := issuer(t)
	signals, exports := newHTTPDiagnosticCollector(t)
	settings := append(append([]string{}, auth...), exports...)
	cert := identity(t, "localhost")
	directory := filepath.Dir(cert.config.CAFile)
	settings = append(settings, "OTEL_SERVICE_NAME=hypershell-sdk-api", "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	for _, entry := range exports {
		name, value, _ := strings.Cut(entry, "=")
		t.Setenv(name, value)
	}
	t.Setenv("OTEL_SERVICE_NAME", "hypershell-sdk")
	binary := buildApplication(t)
	stop, address, rpcAddress := startBoth(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stop() }()
	var backend atomic.Value
	backend.Store(address)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, err := url.Parse(backend.Load().(string))
		if err != nil {
			t.Error(err)
			return
		}
		httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
	}))
	proxy.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	proxy.StartTLS()
	defer proxy.Close()
	ca := filepath.Join(t.TempDir(), "api-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	ownerToken := token(t, key, "alice", "gateway:creator")
	otherToken := token(t, key, "mallory")
	makeClient := func(bearer string) *sdk.Client {
		t.Helper()
		client, err := sdk.NewClient(sdk.Options{BaseURL: proxy.URL, CAFile: ca, Token: bearer})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(client.Close)
		return client
	}
	owner := makeClient(ownerToken)
	other := makeClient(otherToken)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	dns := []string{"private-sdk.example.test"}
	input := sdk.CreateGatewayJSONRequestBody{Name: "private-sdk-gateway", ClusterId: f.cluster, ReleaseId: f.release, DatabaseId: "", ServerDnsNames: &dns}
	created, err := owner.CreateGatewayWithResponse(ctx, input)
	if err != nil || created.JSON201 == nil || created.JSON201.Id == nil {
		t.Fatal("SDK creation failed", err)
	}
	gateway := created.JSON201
	id := *gateway.Id
	parsed, err := ksuid.Parse(id)
	if err != nil || gateway.Namespace == nil || *gateway.Namespace != "openshell-"+hex.EncodeToString(parsed.Payload()[:8]) || gateway.DatabaseId == "" || gateway.CreatedAt == nil || gateway.UpdatedAt == nil {
		t.Fatal("SDK lost assigned Gateway fields")
	}
	if gateway.ServerDnsNames == nil || len(*gateway.ServerDnsNames) != 1 || (*gateway.ServerDnsNames)[0] != dns[0] {
		t.Fatal("SDK changed the DNS array")
	}
	var grants int
	if err := f.db.QueryRow(`SELECT count(*) FROM role_bindings b JOIN roles r ON r.id=b.role_id JOIN users u ON u.id=b.user_id WHERE b.gateway_id=$1 AND b.scope='gateway' AND r.name='gateway:owner' AND u.username='alice'`, id).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("SDK creation lost its owner grant", err)
	}
	readEvent(t, consumer, id)
	owner.Close()
	owner = makeClient(token(t, key, "alice"))
	checkRead := func() {
		t.Helper()
		got, err := owner.GetGatewayWithResponse(ctx, id)
		if err != nil || got.JSON200 == nil || got.JSON200.Id == nil || *got.JSON200.Id != id || got.JSON200.Name != input.Name {
			t.Fatal("SDK retained read failed", err)
		}
		denied, err := other.GetGatewayWithResponse(ctx, id)
		if err != nil || denied.StatusCode() != 404 || denied.JSON404 == nil {
			t.Fatal("SDK denied read lost its error contract", err)
		}
		size := 1
		search := "name = 'private-sdk-gateway'"
		for _, item := range []struct {
			client *sdk.Client
			total  int
		}{{owner, 1}, {other, 0}} {
			list, err := item.client.ListGatewaysWithResponse(ctx, &sdk.ListGatewaysParams{Size: &size, Search: &search})
			if err != nil || list.JSON200 == nil || list.JSON200.Total == nil || *list.JSON200.Total != item.total || list.JSON200.Items == nil || len(*list.JSON200.Items) != item.total {
				t.Fatal("SDK filtered list differs", err)
			}
		}
		rpc, connection := grpcClient(t, rpcAddress, cert)
		defer connection.Close()
		call := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+ownerToken))
		gotRPC, err := rpc.GetGateway(call, &pb.GetGatewayRequest{Id: id})
		if err != nil || gotRPC.GetGateway().GetMetadata().GetId() != id || len(gotRPC.Gateway.ServerDnsNames) != 1 {
			t.Fatal("SDK and gRPC Gateway differ", err)
		}
	}
	checkRead()
	// Synchronize the creator before the test rejects its final event write.
	creator := makeClient(ownerToken)
	if _, err := creator.GetGatewayWithResponse(ctx, id); err != nil {
		t.Fatal(err)
	}
	beforeGrants := count(t, f.db, "role_bindings")
	beforeDatabases := count(t, f.db, "managed_databases")
	awaitQueueEmpty(t, f)
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_sdk_event CHECK (kind <> 'gateway.created') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	failed := input
	failed.Name = "private-sdk-rollback"
	response, err := creator.CreateGatewayWithResponse(ctx, failed)
	if err != nil || response.StatusCode() != 500 || response.JSON500 == nil {
		t.Fatal("SDK lost creation failure status", err)
	}
	if count(t, f.db, "gateways") != 1 || count(t, f.db, "role_bindings") != beforeGrants || count(t, f.db, "managed_databases") != beforeDatabases {
		t.Fatal("SDK failed creation left records")
	}
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_sdk_event"); err != nil {
		t.Fatal(err)
	}
	creator.Close()
	stop()
	stop, address, rpcAddress = startBoth(t, binary, f.dsn, brokerConfig, settings...)
	backend.Store(address)
	checkRead()
	image := "example.test/sdk-gateway:v2"
	updated, err := owner.UpdateGatewayWithResponse(ctx, id, sdk.UpdateGatewayJSONRequestBody{Image: &image})
	if err != nil || updated.JSON200 == nil || updated.JSON200.Image == nil || *updated.JSON200.Image != image {
		t.Fatal("SDK update failed", err)
	}
	readGatewayEvent(t, consumer, id, "Update", "gateway.updated")
	// A missing collector must not prevent a typed API call or bounded close.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unavailable := listener.Addr().String()
	listener.Close()
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://"+unavailable)
	started := time.Now()
	isolated := makeClient(ownerToken)
	retained, err := isolated.GetGatewayWithResponse(ctx, id)
	isolated.Close()
	if err != nil || retained.JSON200 == nil || time.Since(started) > 6*time.Second {
		t.Fatal("collector loss stopped the SDK", err)
	}
	deleted, err := owner.DeleteGatewayWithResponse(ctx, id)
	if err != nil || deleted.StatusCode() != 204 {
		t.Fatal("SDK deletion failed", err)
	}
	readGatewayEvent(t, consumer, id, "Delete", "gateway.deleted")
	missing, err := owner.GetGatewayWithResponse(ctx, id)
	if err != nil || missing.StatusCode() != 404 {
		t.Fatal("SDK read a deleted Gateway", err)
	}
	owner.Close()
	other.Close()
	stop()
	checkSDKSignals(t, signals, []string{ownerToken, otherToken, id, input.Name, failed.Name, dns[0], image, f.dsn, "reject_sdk_event"})
}

func checkSDKSignals(t *testing.T, c *httpDiagnosticCollector, private []string) {
	t.Helper()
	check := func(message proto.Message) {
		data, err := proto.Marshal(message)
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range private {
			if bytes.Contains(data, []byte(value)) {
				t.Fatal("SDK telemetry exposed private data")
			}
		}
	}
	spans := map[string]*tracepb.Span{}
	clients := map[string]*tracepb.Span{}
	logs := map[string]*logpb.LogRecord{}
	measured := map[string]bool{}
	for len(c.traces.received) > 0 {
		batch := <-c.traces.received
		check(batch)
		for _, resource := range batch.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					key := hex.EncodeToString(span.SpanId)
					spans[key] = span
					if scope.Scope.Name == "stego/http-client" {
						telemetryInstance(t, resource.Resource.Attributes, "hypershell-sdk")
						clients[key] = span
					}
				}
			}
		}
	}
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
	for len(c.metrics.received) > 0 {
		batch := <-c.metrics.received
		check(batch)
		for _, resource := range batch.ResourceMetrics {
			for _, scope := range resource.ScopeMetrics {
				if scope.Scope.Name == "stego/http-client" {
					for _, metric := range scope.Metrics {
						if metric.Name == "http.client.request.duration" {
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
	found := map[string]bool{}
	for _, server := range spans {
		if server.Kind != tracepb.Span_SPAN_KIND_SERVER {
			continue
		}
		key := hex.EncodeToString(server.ParentSpanId)
		client, record := clients[key], logs[key]
		if client == nil || record == nil {
			continue
		}
		if client.Kind != tracepb.Span_SPAN_KIND_CLIENT || record.EventName != "http.client.request.completed" || !bytes.Equal(client.TraceId, server.TraceId) || !bytes.Equal(client.TraceId, record.TraceId) {
			t.Fatal("SDK trace and log do not match the API span")
		}
		outcome := signalAttribute(client.Attributes, "outcome").GetStringValue()
		if outcome != signalAttribute(record.Attributes, "outcome").GetStringValue() {
			t.Fatal("SDK log and span outcomes differ")
		}
		found[outcome] = true
	}
	for _, outcome := range []string{"success", "failure"} {
		if !found[outcome] || !measured[outcome] {
			t.Fatal("SDK request has no complete trace, log, and metric", outcome)
		}
	}
}
