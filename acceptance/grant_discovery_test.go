package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jsell-rh/hypershell-stego/contracts"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
)

type grantListResponse struct {
	Kind, Href string
	Page, Size int
	Total      int64
	Items      []grantResponse
}

type grantWatch struct {
	stream pb.RoleBindingService_WatchRoleBindingsClient
	cancel context.CancelFunc
}

func watchGrants(t testing.TB, client pb.RoleBindingServiceClient, ctx context.Context) *grantWatch {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	t.Cleanup(cancel)
	stream, err := client.WatchRoleBindings(ctx, &pb.WatchRoleBindingsRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Header(); err != nil {
		t.Fatal(err)
	}
	return &grantWatch{stream: stream, cancel: cancel}
}
func (w *grantWatch) expect(t testing.TB, kind pb.EventType, id, role, username string) *pb.RoleBinding {
	t.Helper()
	event, err := w.stream.Recv()
	if err != nil || event.GetType() != kind || event.GetResourceId() != id {
		t.Fatalf("grant watch: %v %v, want %v %s", event, err, kind, id)
	}
	row := event.RoleBinding
	if row.GetMetadata().GetId() != id || row.GetRoleName() != role || row.GetUsername() != username || row.GetMetadata().GetKind() != "RoleBinding" || row.GetMetadata().GetHref() != "/api/hypershell/v1/role_bindings/"+id || row.GetMetadata().GetCreatedAt().CheckValid() != nil || row.GetMetadata().GetUpdatedAt().CheckValid() != nil {
		t.Fatalf("grant shape: %v", row)
	}
	return row
}

func TestGrantDiscoveryThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	_, connection := grpcClient(t, grpcAddress, tlsIdentity)
	client := pb.NewRoleBindingServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	call := func(who string, roles ...string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, who, roles...)))
	}
	owner := token(t, key, "alice")
	base := address + "/api/hypershell/v1"
	unauthenticated, err := client.WatchRoleBindings(ctx, &pb.WatchRoleBindingsRequest{})
	if err == nil {
		_, err = unauthenticated.Recv()
	}
	if status.Code(err) != codes.Unauthenticated {
		t.Fatal("watch authentication", err)
	}
	watch := watchGrants(t, client, call("alice"))
	encoded, err := json.Marshal(f.request("grant-discovery"))
	if err != nil {
		t.Fatal(err)
	}
	code, body := requestJSON(t, "POST", base+"/gateways", token(t, key, "alice", "gateway:creator"), encoded)
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &gateway) != nil {
		t.Fatal("create", code, string(body))
	}
	reference, err := contracts.Load(ctx)
	if err != nil {
		t.Fatal(err)
	}
	schema := reference.OpenAPI.Paths.Value("/api/hypershell/v1/role_bindings").Get.Responses.Status(200).Value.Content.Get("application/json").Schema.Value
	listREST := func(who string, total int64, query string) grantListResponse {
		t.Helper()
		code, body := requestJSON(t, "GET", base+"/role_bindings"+query, token(t, key, who), nil)
		var raw any
		if err := json.Unmarshal(body, &raw); err != nil {
			t.Fatal(err)
		}
		if err := schema.VisitJSON(raw); err != nil {
			t.Fatal("reference list shape", err)
		}
		var list grantListResponse
		if code != 200 || json.Unmarshal(body, &list) != nil || list.Total != total || list.Size != len(list.Items) || list.Kind != "RoleBindingList" || list.Href != "/api/hypershell/v1/role_bindings" {
			t.Fatal("grant list", code, string(body))
		}
		return list
	}
	initial := listREST("alice", 1, "")
	ownerGrant := initial.Items[0]
	watch.expect(t, pb.EventType_EVENT_TYPE_CREATED, ownerGrant.ID, "gateway:owner", "alice")
	readGrantEvent(t, consumer, ownerGrant.ID, gateway.ID, "Create", "rolebinding.created")
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	create := func(input gateways.GrantRequest) grantResponse {
		t.Helper()
		encoded, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		code, body := requestJSON(t, "POST", base+"/role_bindings", owner, encoded)
		var row grantResponse
		if code != 201 || json.Unmarshal(body, &row) != nil {
			t.Fatal("grant create", code, string(body))
		}
		return row
	}
	viewer := create(input)
	watch.expect(t, pb.EventType_EVENT_TYPE_CREATED, viewer.ID, "gateway:viewer", "bob")
	listREST("alice", 2, "")
	listREST("bob", 1, "")
	listREST("mallory", 0, "")
	code, body = requestJSON(t, "GET", base+"/role_bindings", token(t, key, "admin", "platform:admin"), nil)
	var admin grantListResponse
	if code != 200 || json.Unmarshal(body, &admin) != nil || admin.Total != 0 {
		t.Fatal("admin bypass", code, string(body))
	}
	first := listREST("alice", 2, "?size=1&orderBy=id%20asc")
	second := listREST("alice", 2, "?size=1&page=2&orderBy=id%20asc")
	if len(first.Items) != 1 || len(second.Items) != 1 || first.Items[0].ID == second.Items[0].ID {
		t.Fatal("unstable pages")
	}
	counted := listREST("alice", 2, "?size=0")
	if len(counted.Items) != 0 {
		t.Fatal("count-only returned rows")
	}
	listREST("bob", 1, "?search="+url.QueryEscape("user_id = '"+input.UserID+"' or user_id = '"+ownerGrant.UserID+"'"))
	for _, query := range []string{"?size=101", "?page=0", "?size=1&size=2", "?orderBy=unknown", "?search=" + url.QueryEscape("unknown = 'x'"), "?fields=id"} {
		if code, _ := requestJSON(t, "GET", base+"/role_bindings"+query, owner, nil); code != 400 {
			t.Fatal("invalid list query", query, code)
		}
	}
	request := &pb.ListRoleBindingsRequest{UserId: &input.UserID, GatewayId: &gateway.ID}
	got, err := client.ListRoleBindings(call("alice"), request)
	if err != nil || len(got.Items) != 1 || got.Items[0].Metadata.Id != viewer.ID || got.Items[0].RoleName != "gateway:viewer" || got.Items[0].Username != "bob" {
		t.Fatal("gRPC list", got, err)
	}
	denied, err := client.ListRoleBindings(call("mallory"), request)
	if err != nil || len(denied.Items) != 0 {
		t.Fatal("gRPC visibility", denied, err)
	}
	for _, req := range []*pb.ListRoleBindingsRequest{{}, {UserId: proto.String("invalid")}, {UserId: &input.UserID, GatewayId: proto.String("")}} {
		if _, err := client.ListRoleBindings(call("alice"), req); status.Code(err) != codes.InvalidArgument {
			t.Fatal("gRPC input", err)
		}
	}
	if _, err := client.ListRoleBindings(ctx, request); status.Code(err) != codes.Unauthenticated {
		t.Fatal("missing auth", err)
	}
	viewerWatch := watchGrants(t, client, call("bob"))
	viewerWatch.expect(t, pb.EventType_EVENT_TYPE_UPDATED, viewer.ID, "gateway:viewer", "bob")
	// A false deletion notice must not return a live row as deleted.
	notice, _ := json.Marshal(map[string]string{"id": uuid.NewString(), "destination": "kafka", "resource_key": viewer.ID, "kind": "rolebinding.deleted"})
	if _, err := f.db.Exec(`SELECT pg_notify('stego_resource_events_v1',$1)`, string(notice)); err != nil {
		t.Fatal(err)
	}
	// A failed event write must not leave a grant or a watch notice.
	other := grantInput(t, f, gateway.ID, "charlie", "gateway:viewer")
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_discovery CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	encoded, _ = json.Marshal(other)
	if code, _ := requestJSON(t, "POST", base+"/role_bindings", owner, encoded); code != 500 {
		t.Fatal("rollback", code)
	}
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_discovery`); err != nil {
		t.Fatal(err)
	}
	listREST("alice", 2, "")
	if code, _ := requestJSON(t, "DELETE", base+"/role_bindings/"+viewer.ID, owner, nil); code != 204 {
		t.Fatal("remove", code)
	}
	watch.expect(t, pb.EventType_EVENT_TYPE_DELETED, viewer.ID, "gateway:viewer", "bob")
	viewerWatch.expect(t, pb.EventType_EVENT_TYPE_DELETED, viewer.ID, "gateway:viewer", "bob")
	listREST("bob", 0, "")
	// A later grant to another user must not reach the removed viewer.
	charlie := create(other)
	watch.expect(t, pb.EventType_EVENT_TYPE_CREATED, charlie.ID, "gateway:viewer", "charlie")
	restored := create(input)
	watch.expect(t, pb.EventType_EVENT_TYPE_CREATED, restored.ID, "gateway:viewer", "bob")
	viewerWatch.expect(t, pb.EventType_EVENT_TYPE_CREATED, restored.ID, "gateway:viewer", "bob")
	readGrantEvent(t, consumer, restored.ID, gateway.ID, "Create", "rolebinding.created")
	awaitQueueEmpty(t, f)
	stop()
	if _, err := watch.stream.Recv(); err == nil {
		t.Fatal("watch survived stop")
	}
	if _, err := viewerWatch.stream.Recv(); err == nil {
		t.Fatal("viewer received hidden event")
	}
	watch.cancel()
	viewerWatch.cancel()
	if err := f.service.DeleteGrant(ctx, principal("alice"), restored.ID); err != nil {
		t.Fatal(err)
	}
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	base = address + "/api/hypershell/v1"
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	client = pb.NewRoleBindingServiceClient(connection)
	replay := watchGrants(t, client, call("alice"))
	ids := []string{}
	for range 2 {
		event, err := replay.stream.Recv()
		if err != nil || event.Type != pb.EventType_EVENT_TYPE_UPDATED {
			t.Fatal("replay", event, err)
		}
		ids = append(ids, event.ResourceId)
	}
	slices.Sort(ids)
	want := []string{ownerGrant.ID, charlie.ID}
	slices.Sort(want)
	if !slices.Equal(ids, want) {
		t.Fatal("replay included deleted or hidden grants", ids, want)
	}
	listREST("bob", 0, "")
	got, err = client.ListRoleBindings(call("controller"), request)
	if err != nil || len(got.Items) != 0 {
		t.Fatal("restart list", got, err)
	}
	if code, _ := requestJSON(t, "DELETE", base+"/role_bindings/"+charlie.ID, owner, nil); code != 204 {
		t.Fatal(code)
	}
	replay.expect(t, pb.EventType_EVENT_TYPE_DELETED, charlie.ID, "gateway:viewer", "charlie")
	readGrantEvent(t, consumer, restored.ID, gateway.ID, "Delete", "rolebinding.deleted")
	awaitQueueEmpty(t, f)
}

func TestGrantDiscoveryUsesCurrentRolesAndLiveGateways(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("shared"))
	if err != nil {
		t.Fatal(err)
	}
	grant, err := f.service.CreateGrant(ctx, principal("alice"), grantInput(t, f, gateway.ID, "bob", "gateway:viewer"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := gateways.New(f.storage, gateways.Options{ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.EventGrant(ctx, principal("bob"), grant.ID, true); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("false deletion", err)
	}
	if _, err := f.db.Exec("UPDATE roles SET deleted_at=now() WHERE id=$1", grant.RoleID); err != nil {
		t.Fatal(err)
	}
	if rows, err := service.AllGrants(ctx, principal("controller"), grant.UserID, ""); err == nil || rows != nil {
		t.Fatal("partial role list", rows, err)
	}
	if _, err := f.db.Exec("UPDATE roles SET deleted_at=NULL WHERE id=$1", grant.RoleID); err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(ctx, principal("alice"), gateway.ID); err != nil {
		t.Fatal(err)
	}
	for _, who := range []string{"alice", "bob", "controller"} {
		page, err := service.ListGrants(ctx, principal(who), gateways.GrantQuery{Page: 1, Size: 100})
		if err != nil || page.Total != 0 || len(page.Items) != 0 {
			t.Fatal("deleted Gateway grants", who, page, err)
		}
	}
}

func TestGeneratedGrantDescriptorMatchesReference(t *testing.T) {
	reference, err := contracts.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	actual := pb.File_hypershell_v1_role_bindings_proto
	expected := protodesc.ToFileDescriptorProto(reference.Proto.FindFileByPath(actual.Path()))
	got := protodesc.ToFileDescriptorProto(actual)
	got.Options.GoPackage = nil
	expected.Options.GoPackage = nil
	got.SourceCodeInfo = nil
	expected.SourceCodeInfo = nil
	if !proto.Equal(got, expected) {
		t.Fatal("generated grant wire descriptor differs")
	}
}

func TestGrantDiscoveryRejectsOversizedGRPCResponse(t *testing.T) {
	f := database(t)
	gateway, err := f.service.Create(context.Background(), principal("alice", "gateway:creator"), f.request("load-anchor"))
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	// Each grant refers to one live Gateway. A long valid profile makes the
	// complete response exceed the byte limit before it reaches the row limit.
	if _, err := f.db.Exec("UPDATE users SET username=$1 WHERE id=$2", strings.Repeat("b", 255), input.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO gateways(id,name,cluster_id,release_id,database_id,namespace,created_time,updated_time)
 SELECT lpad(n::text,27,'0'),'load', $1,$2,$3,'load-'||n,now(),now() FROM generate_series(1,7000) n`, f.cluster, f.release, f.database); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO role_bindings(id,user_id,role_id,gateway_id,scope,created_time,updated_time)
 SELECT lpad(n::text,27,'0'),$1,$2,lpad(n::text,27,'0'),'gateway',now(),now() FROM generate_series(1,7000) n`, input.UserID, input.RoleID); err != nil {
		t.Fatal(err)
	}
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	stop, _, address := startBoth(t, buildApplication(t), f.dsn, config, settings...)
	defer stop()
	_, connection := grpcClient(t, address, tlsIdentity)
	client := pb.NewRoleBindingServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "controller")))
	result, err := client.ListRoleBindings(ctx, &pb.ListRoleBindingsRequest{UserId: &input.UserID})
	if status.Code(err) != codes.ResourceExhausted || status.Convert(err).Message() != "grant response exceeds its resource limit" || result != nil {
		t.Fatal("oversized response", result, err)
	}
	// The same endpoint can still return a complete bounded result.
	result, err = client.ListRoleBindings(ctx, &pb.ListRoleBindingsRequest{UserId: &input.UserID, GatewayId: proto.String(strings.Repeat("0", 26) + "1")})
	if err != nil || len(result.Items) != 1 {
		t.Fatal("filtered response", result, err)
	}
}
