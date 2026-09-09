package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/contracts"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGatewayMutationWorkflowAcrossTransportsAndRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	binary := buildApplication(t)
	stop, httpAddress, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	client, connection := grpcClient(t, grpcAddress, tlsIdentity)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	owner := token(t, key, "alice")
	creator := token(t, key, "alice", "gateway:creator")
	controller := token(t, key, "controller")
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	path := httpAddress + "/api/hypershell/v1/gateways"
	code, data := requestJSON(t, "POST", path, creator, []byte(fmt.Sprintf(`{"name":"original","cluster_id":%q,"release_id":%q,"database_id":"ignored","server_dns_names":["old.example.test"]}`, f.cluster, f.release)))
	var original httpapi.Gateway
	if code != 201 || json.Unmarshal(data, &original) != nil {
		t.Fatalf("create: %d %s", code, data)
	}
	messageIDs := map[string]bool{}
	event := func(eventType, kind string) {
		id := readGatewayEvent(t, consumer, original.ID, eventType, kind)
		if id == "" || messageIDs[id] {
			t.Fatal("event identifier is missing or repeated")
		}
		messageIDs[id] = true
		awaitQueueEmpty(t, f)
	}
	event("Create", "gateway.created")
	code, data = requestJSON(t, "PATCH", path+"/"+original.ID, owner, []byte(`{"name":"rest-patch","database_id":"not-placement","external_dns":"","tls_mode":"passthrough","service_type":"ClusterIP","status":"Ready","phase":"Ready","image":"gateway:v2","supervisor_image":"supervisor:v2","server_dns_names":["new.example.test"],"route_address":"gateway.example.test","oidc":"{}","route":"{}","credential_driver":"driver-a"}`))
	var patched httpapi.Gateway
	if code != 200 || json.Unmarshal(data, &patched) != nil {
		t.Fatalf("REST patch: %d %s", code, data)
	}
	if patched.ID != original.ID || patched.Name != "rest-patch" || patched.DatabaseID != original.DatabaseID || patched.Namespace != original.Namespace || !patched.CreatedAt.Equal(original.CreatedAt) || !patched.UpdatedAt.After(original.UpdatedAt) || patched.CreatedBy != "alice" {
		t.Fatalf("REST patch shape: %s", data)
	}
	reference, err := contracts.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		t.Fatal(err)
	}
	schema := reference.OpenAPI.Paths.Value("/api/hypershell/v1/gateways/{id}").Patch.Responses.Status(200).Value.Content.Get("application/json").Schema.Value
	if err := schema.VisitJSON(value); err != nil {
		t.Fatalf("patch response violates contract: %v", err)
	}
	event("Update", "gateway.updated")
	got, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: original.ID})
	if err != nil || got.Gateway.Name != "rest-patch" || got.Gateway.GetCredentialDriver() != "driver-a" || got.Gateway.GetSupervisorImage() != "supervisor:v2" || got.Gateway.GetExternalDns() != "" || len(got.Gateway.ServerDnsNames) != 1 || got.Gateway.ServerDnsNames[0] != "new.example.test" {
		t.Fatalf("gRPC read after REST patch: %v %v", got, err)
	}
	updated, err := client.UpdateGateway(call(owner), &pb.UpdateGatewayRequest{Id: original.ID, Name: pointer("grpc-patch"), SupervisorImage: pointer("supervisor:v3"), CredentialDriver: pointer("driver-a"), ServerDnsNames: []string{}, DatabaseId: pointer("ignored")})
	if err != nil || updated.Gateway.GetSupervisorImage() != "supervisor:v3" || updated.Gateway.GetCredentialDriver() != "driver-a" || len(updated.Gateway.ServerDnsNames) != 1 {
		t.Fatalf("gRPC patch: %v %v", updated, err)
	}
	event("Update", "gateway.updated")
	code, data = requestJSON(t, "GET", path+"/"+original.ID, owner, nil)
	if code != 200 || json.Unmarshal(data, &patched) != nil || patched.Name != "grpc-patch" || patched.SupervisorImage == nil || *patched.SupervisorImage != "supervisor:v3" {
		t.Fatalf("REST read after gRPC patch: %d %s", code, data)
	}
	// All omitted and null fields stay unchanged. Empty DNS lists also stay unchanged.
	code, data = requestJSON(t, "PATCH", path+"/"+original.ID, owner, []byte(`{"name":null,"server_dns_names":[]}`))
	if code != 200 || json.Unmarshal(data, &patched) != nil || patched.Name != "grpc-patch" || len(patched.ServerDNSNames) != 1 {
		t.Fatalf("optional patch values: %d %s", code, data)
	}
	event("Update", "gateway.updated")
	for _, body := range []string{`{"namespace":"wrong"}`, `{"console_address":"https://wrong.example"}`, `{"active_sandbox_count":90}`, `{"Name":"alias"}`, `{"name":"a","name":"b"}`, `{"name":""}`, `{"release_id":"missing"}`, `{"route_address":"bad\u0000"}`} {
		code, data = requestJSON(t, "PATCH", path+"/"+original.ID, owner, []byte(body))
		if code != 400 {
			t.Fatalf("invalid patch accepted: %d %s", code, data)
		}
	}
	code, data = requestJSON(t, "PATCH", path+"/"+original.ID, owner, []byte(`{"credential_driver":"driver-b"}`))
	if code != 409 {
		t.Fatalf("driver conflict: %d %s", code, data)
	}
	if _, err := client.UpdateGateway(call(owner), &pb.UpdateGatewayRequest{Id: original.ID, CredentialDriver: pointer("driver-b")}); status.Code(err) != codes.AlreadyExists {
		t.Fatalf("gRPC driver conflict: %v", err)
	}
	if _, err := client.UpdateGateway(call(owner), &pb.UpdateGatewayRequest{Id: original.ID, ConsoleAddress: pointer("https://wrong.example")}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("owner changed console: %v", err)
	}
	if _, err := client.UpdateGateway(call(owner), &pb.UpdateGatewayRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing update ID: %v", err)
	}
	if _, err := client.DeleteGateway(call(owner), &pb.DeleteGatewayRequest{}); status.Code(err) != codes.InvalidArgument {
		t.Fatalf("missing delete ID: %v", err)
	}
	for _, bearer := range []string{token(t, key, "bob", "gateway:creator"), token(t, key, "mallory", "gateway:owner"), token(t, key, "admin", "platform:admin")} {
		code, data = requestJSON(t, "PATCH", path+"/"+original.ID, bearer, []byte(`{"name":"denied"}`))
		if code != 404 {
			t.Fatalf("unowned REST patch: %d %s", code, data)
		}
		if _, err := client.UpdateGateway(call(bearer), &pb.UpdateGatewayRequest{Id: original.ID, Name: pointer("denied")}); status.Code(err) != codes.NotFound {
			t.Fatalf("unowned gRPC patch: %v", err)
		}
	}
	for _, bearer := range []string{token(t, key, "bob", "gateway:creator"), token(t, key, "mallory", "gateway:owner")} {
		code, data = requestJSON(t, "DELETE", path+"/"+original.ID, bearer, nil)
		if code != 404 {
			t.Fatalf("unowned REST delete: %d %s", code, data)
		}
		if _, err := client.DeleteGateway(call(bearer), &pb.DeleteGatewayRequest{Id: original.ID}); status.Code(err) != codes.NotFound {
			t.Fatalf("unowned gRPC delete: %v", err)
		}
	}
	viewer := token(t, key, "viewer")
	if _, err := client.ListGateways(call(viewer), &pb.ListGatewaysRequest{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO role_bindings(id,user_id,role_id,gateway_id,scope) SELECT $1,u.id,r.id,$2,'gateway' FROM users u,roles r WHERE u.username='viewer' AND r.name='gateway:viewer'`, ksuid.New().String(), original.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.GetGateway(call(viewer), &pb.GetGatewayRequest{Id: original.ID}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		var body []byte
		if method == "PATCH" {
			body = []byte(`{"name":"denied"}`)
		}
		code, data = requestJSON(t, method, path+"/"+original.ID, viewer, body)
		if code != 404 {
			t.Fatalf("viewer mutation: %d %s", code, data)
		}
	}
	if _, err := client.UpdateGateway(call(viewer), &pb.UpdateGatewayRequest{Id: original.ID, Name: pointer("denied")}); status.Code(err) != codes.NotFound {
		t.Fatalf("viewer gRPC patch: %v", err)
	}
	if _, err := client.DeleteGateway(call(viewer), &pb.DeleteGatewayRequest{Id: original.ID}); status.Code(err) != codes.NotFound {
		t.Fatalf("viewer gRPC delete: %v", err)
	}
	for _, test := range []struct {
		suffix string
		body   []byte
	}{{"", []byte(`{}`)}, {"?force=true", nil}} {
		code, data = requestJSON(t, "DELETE", path+"/"+original.ID+test.suffix, owner, test.body)
		if code != 400 {
			t.Fatalf("invalid delete accepted: %d %s", code, data)
		}
	}
	code, _ = requestJSON(t, "PATCH", path+"/"+original.ID, "", []byte(`{"name":"denied"}`))
	if code != 401 {
		t.Fatal("unauthenticated patch accepted")
	}
	code, _ = requestJSON(t, "DELETE", path+"/"+original.ID, "", nil)
	if code != 401 {
		t.Fatal("unauthenticated delete accepted")
	}
	if _, err := client.UpdateGateway(ctx, &pb.UpdateGatewayRequest{Id: original.ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated gRPC update: %v", err)
	}
	if _, err := client.DeleteGateway(ctx, &pb.DeleteGatewayRequest{Id: original.ID}); status.Code(err) != codes.Unauthenticated {
		t.Fatalf("unauthenticated gRPC delete: %v", err)
	}
	// Only the configured subject can set the console address.
	observed, err := control.NewGatewayIdentityServiceClient(connection).GetGatewayIdentityState(call(controller), &control.GetGatewayIdentityStateRequest{Id: original.ID})
	if err != nil {
		t.Fatal(err)
	}
	conditional, err := rpc.WithResourceVersion(call(controller), observed.ResourceVersion)
	if err != nil {
		t.Fatal(err)
	}
	updated, err = client.UpdateGateway(conditional, &pb.UpdateGatewayRequest{Id: original.ID, ConsoleAddress: pointer("https://console.example.test")})
	if err != nil || updated.Gateway.GetConsoleAddress() != "https://console.example.test" {
		t.Fatalf("controller update: %v %v", updated, err)
	}
	event("Update", "gateway.updated")
	// Both transports must roll back when event storage fails.
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_mutations CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"PATCH", "DELETE"} {
		var body []byte
		if method == "PATCH" {
			body = []byte(`{"name":"must roll back"}`)
		}
		code, data = requestJSON(t, method, path+"/"+original.ID, owner, body)
		if code != 500 || strings.Contains(string(data), "reject_mutations") {
			t.Fatalf("REST storage failure: %d %s", code, data)
		}
	}
	if _, err := client.UpdateGateway(call(owner), &pb.UpdateGatewayRequest{Id: original.ID, Name: pointer("must roll back")}); status.Code(err) != codes.Internal || strings.Contains(err.Error(), "reject_mutations") {
		t.Fatalf("gRPC update failure: %v", err)
	}
	if _, err := client.DeleteGateway(call(owner), &pb.DeleteGatewayRequest{Id: original.ID}); status.Code(err) != codes.Internal || strings.Contains(err.Error(), "reject_mutations") {
		t.Fatalf("gRPC delete failure: %v", err)
	}
	got, err = client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: original.ID})
	if err != nil || got.Gateway.Name != "grpc-patch" {
		t.Fatalf("failure changed row: %v %v", got, err)
	}
	if count(t, f.db, "stego_outbox.messages") != 0 {
		t.Fatal("failure left an event")
	}
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_mutations`); err != nil {
		t.Fatal(err)
	}
	connection.Close()
	stop()
	// The update and delete events are durable while the runtime is stopped.
	if _, err := f.service.Update(ctx, principal("alice"), original.ID, gateways.PatchRequest{Phase: pointer("Stopping")}); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Delete(ctx, principal("alice"), original.ID); err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "stego_outbox.messages") != 2 {
		t.Fatal("offline mutations lost events")
	}
	stop, httpAddress, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	client, connection = grpcClient(t, grpcAddress, tlsIdentity)
	// Events for one resource retain commit order after restart.
	first := readGatewayEvent(t, consumer, original.ID, "Update", "gateway.updated")
	second := readGatewayEvent(t, consumer, original.ID, "Delete", "gateway.deleted")
	if first == "" || second == "" || first == second {
		t.Fatal("restart lost event IDs")
	}
	awaitQueueEmpty(t, f)
	path = httpAddress + "/api/hypershell/v1/gateways"
	if _, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: original.ID}); status.Code(err) != codes.NotFound {
		t.Fatalf("restart restored deleted row: %v", err)
	}
	code, _ = requestJSON(t, "GET", path+"/"+original.ID, owner, nil)
	if code != 404 {
		t.Fatal("REST restored deleted row")
	}
	if list, err := client.ListGateways(call(owner), &pb.ListGatewaysRequest{}); err != nil || list.Metadata.Total != 0 {
		t.Fatalf("deleted row counted: %v %v", list, err)
	}
	// Exercise successful deletes through both public transports.
	for _, transport := range []string{"REST", "gRPC"} {
		created, err := client.CreateGateway(call(creator), &pb.CreateGatewayRequest{Name: "delete-" + transport, ClusterId: f.cluster, ReleaseId: f.release})
		if err != nil {
			t.Fatal(err)
		}
		id := created.Gateway.Metadata.Id
		readEvent(t, consumer, id)
		awaitQueueEmpty(t, f)
		if transport == "REST" {
			code, data = requestJSON(t, "DELETE", path+"/"+id, owner, nil)
			if code != 204 || len(data) != 0 {
				t.Fatalf("delete response: %d %s", code, data)
			}
		} else {
			if _, err := client.DeleteGateway(call(token(t, key, "admin", "platform:admin")), &pb.DeleteGatewayRequest{Id: id}); err != nil {
				t.Fatal(err)
			}
		}
		readGatewayEvent(t, consumer, id, "Delete", "gateway.deleted")
		awaitQueueEmpty(t, f)
		if _, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: id}); status.Code(err) != codes.NotFound {
			t.Fatalf("delete did not persist: %v", err)
		}
	}
	if _, err := client.DeleteGateway(call(owner), &pb.DeleteGatewayRequest{Id: ksuid.New().String()}); status.Code(err) != codes.NotFound {
		t.Fatalf("missing delete: %v", err)
	}
	connection.Close()
	stop()
}
