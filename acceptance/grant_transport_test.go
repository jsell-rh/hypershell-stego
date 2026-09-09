package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type grantResponse struct {
	httpapi.Reference
	RoleID    string `json:"role_id"`
	UserID    string `json:"user_id"`
	GatewayID string `json:"gateway_id"`
	Scope     string `json:"scope"`
}

func TestGatewayGrantWorkflowAcrossTransportsAndRestart(t *testing.T) {
	f := database(t)
	key, settings := issuer(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	owner, viewer, other := token(t, key, "alice", "gateway:creator"), token(t, key, "bob"), token(t, key, "mallory", "gateway:creator")
	base := address + "/api/hypershell/v1"
	encoded, err := json.Marshal(f.request("shared-gateway"))
	if err != nil {
		t.Fatal(err)
	}
	code, body := requestJSON(t, "POST", base+"/gateways", owner, encoded)
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &gateway) != nil {
		t.Fatalf("create Gateway: %d %s", code, body)
	}
	readEvent(t, consumer, gateway.ID)
	// The API creates Bob's identity. The fixture reads the opaque user and role IDs.
	if code, _ := requestJSON(t, "GET", base+"/gateways", viewer, nil); code != 200 {
		t.Fatal("viewer identity request", code)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	encoded, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	client, _ := grpcClient(t, grpcAddress, tlsIdentity)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	watch := watchGateways(t, client, call(viewer))
	visibleBase := int64(0)
	checkAccess := func(want bool) {
		t.Helper()
		wanted := 404
		if want {
			wanted = 200
		}
		if code, _ := requestJSON(t, "GET", base+"/gateways/"+gateway.ID, viewer, nil); code != wanted {
			t.Fatalf("REST access: %d, want %d", code, wanted)
		}
		_, err := client.GetGateway(call(viewer), &pb.GetGatewayRequest{Id: gateway.ID})
		if (want && err != nil) || (!want && status.Code(err) != codes.NotFound) {
			t.Fatal("gRPC access", err)
		}
		code, body := requestJSON(t, "GET", base+"/gateways?size=1", viewer, nil)
		var list httpapi.GatewayList
		wantedTotal := visibleBase
		if want {
			wantedTotal++
		}
		if code != 200 || json.Unmarshal(body, &list) != nil || list.Total != wantedTotal || len(list.Items) != min(1, int(wantedTotal)) {
			t.Fatal("REST filtered list", code, string(body))
		}
		rpcList, err := client.ListGateways(call(viewer), &pb.ListGatewaysRequest{})
		if err != nil || int64(rpcList.Metadata.Total) != wantedTotal || len(rpcList.Items) != int(wantedTotal) {
			t.Fatal("gRPC filtered list", rpcList, err)
		}
	}
	checkAccess(false)
	code, body = requestJSON(t, "POST", base+"/role_bindings", owner, encoded)
	var first grantResponse
	if code != 201 || json.Unmarshal(body, &first) != nil {
		t.Fatalf("create grant: %d %s", code, body)
	}
	if _, err := ksuid.Parse(first.ID); err != nil || first.Kind != "RoleBinding" || first.Href != "/api/hypershell/v1/role_bindings/"+first.ID || first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() || first.UserID != input.UserID || first.RoleID != input.RoleID || first.GatewayID != gateway.ID || first.Scope != "gateway" {
		t.Fatal("grant response shape", first)
	}
	firstMessage := readGrantEvent(t, consumer, first.ID, gateway.ID, "Create", "rolebinding.created")
	watch.expect(t, pb.EventType_EVENT_TYPE_UPDATED, gateway.ID, gateway.Name)
	checkAccess(true)
	if code, _ := requestJSON(t, "POST", base+"/role_bindings", owner, encoded); code != 409 {
		t.Fatal("duplicate grant", code)
	}
	for _, bearer := range []string{viewer, other, token(t, key, "admin", "platform:admin")} {
		if code, _ := requestJSON(t, "POST", base+"/role_bindings", bearer, encoded); code != 404 {
			t.Fatal("non-owner created grant", code)
		}
		if code, _ := requestJSON(t, "DELETE", base+"/role_bindings/"+first.ID, bearer, nil); code != 404 {
			t.Fatal("non-owner deleted grant", code)
		}
	}
	if code, _ := requestJSON(t, "GET", base+"/role_bindings/"+first.ID, viewer, nil); code != 200 {
		t.Fatal("grantee cannot read grant", code)
	}
	if code, _ := requestJSON(t, "GET", base+"/role_bindings/"+first.ID, other, nil); code != 404 {
		t.Fatal("foreign grant disclosed", code)
	}
	if code, _ := requestJSON(t, "PATCH", base+"/gateways/"+gateway.ID, viewer, []byte(`{"name":"denied"}`)); code != 404 {
		t.Fatal("viewer changed Gateway", code)
	}
	var ownerGrant string
	if err := f.db.QueryRow("SELECT id FROM role_bindings WHERE gateway_id=$1 AND id<>$2 AND deleted_at IS NULL", gateway.ID, first.ID).Scan(&ownerGrant); err != nil {
		t.Fatal(err)
	}
	if code, _ := requestJSON(t, "DELETE", base+"/role_bindings/"+ownerGrant, owner, nil); code != 409 {
		t.Fatal("last owner removed", code)
	}
	if code, _ := requestJSON(t, "DELETE", base+"/role_bindings/"+first.ID, owner, nil); code != 204 {
		t.Fatal("remove viewer", code)
	}
	readGrantEvent(t, consumer, first.ID, gateway.ID, "Delete", "rolebinding.deleted")
	checkAccess(false)
	if code, _ := requestJSON(t, "GET", base+"/role_bindings/"+first.ID, owner, nil); code != 404 {
		t.Fatal("deleted grant visible", code)
	}
	// A visible event is a barrier: a revoked Gateway update must not precede it.
	anchorBody, err := json.Marshal(f.request("viewer-anchor"))
	if err != nil {
		t.Fatal(err)
	}
	code, body = requestJSON(t, "POST", base+"/gateways", token(t, key, "bob", "gateway:creator"), anchorBody)
	var anchor httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &anchor) != nil {
		t.Fatal("create watch barrier", code)
	}
	watch.expect(t, pb.EventType_EVENT_TYPE_CREATED, anchor.ID, anchor.Name)
	readEvent(t, consumer, anchor.ID)
	visibleBase = 1
	awaitQueueEmpty(t, f)
	stop()
	// Commit a new grant while event delivery is stopped. Restart must deliver it.
	second, err := f.service.CreateGrant(context.Background(), principal("alice"), input)
	if err != nil || second.ID == first.ID {
		t.Fatal("re-grant", second.ID, err)
	}
	if count(t, f.db, "stego_outbox.messages") != 2 {
		t.Fatal("offline grant events not durable")
	}
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	defer stop()
	base = address + "/api/hypershell/v1"
	client, _ = grpcClient(t, grpcAddress, tlsIdentity)
	secondMessage := readGrantEvent(t, consumer, second.ID, gateway.ID, "Create", "rolebinding.created")
	if firstMessage == secondMessage {
		t.Fatal("new grant reused an event ID")
	}
	awaitQueueEmpty(t, f)
	checkAccess(true)
	// A second REST removal and creation proves key reuse through the public API.
	if code, _ := requestJSON(t, "DELETE", base+"/role_bindings/"+second.ID, owner, nil); code != 204 {
		t.Fatal("remove after restart", code)
	}
	code, body = requestJSON(t, "POST", base+"/role_bindings", owner, encoded)
	var third grantResponse
	if code != 201 || json.Unmarshal(body, &third) != nil || third.ID == first.ID || third.ID == second.ID {
		t.Fatal("REST re-grant", code, string(body))
	}
	checkAccess(true)
}

func readGrantEvent(t *testing.T, consumer *kgo.Client, id, gatewayID, eventType, kind string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		for _, record := range consumer.PollRecords(ctx, 1).Records() {
			if string(record.Key) != id {
				continue
			}
			var payload map[string]string
			if err := json.Unmarshal(record.Value, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 4 || payload["source"] != "RoleBindings" || payload["source_id"] != id || payload["gateway_id"] != gatewayID || payload["event_type"] != eventType {
				t.Fatal("grant event payload", string(record.Value))
			}
			headers := map[string]string{}
			for _, header := range record.Headers {
				headers[header.Key] = string(header.Value)
			}
			if headers["stego-message-kind"] != kind || headers["stego-message-id"] == "" {
				t.Fatal("grant event headers", headers)
			}
			return headers["stego-message-id"]
		}
	}
	t.Fatal("grant event was not delivered", ctx.Err())
	return ""
}
