package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/contracts"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGlobalRolesThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	base := address + "/api/hypershell/v1"
	_, connection := grpcClient(t, grpcAddress, tlsIdentity)
	client := pb.NewRoleBindingServiceClient(connection)
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+bearer))
	}
	plain := token(t, key, "alice")
	creator := token(t, key, "alice", "gateway:creator")
	both := token(t, key, "alice", "gateway:creator", "platform:admin", "gateway:owner", "unselected-role")
	user := currentUser(t, base, plain)
	observer := watchGrants(t, client, call(plain))
	reference, err := contracts.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	schema := reference.OpenAPI.Paths.Value("/api/hypershell/v1/role_bindings").Get.Responses.Status(200).Value.Content.Get("application/json").Schema.Value
	globals := func(bearer string, want int) []grantResponse {
		t.Helper()
		code, body := requestJSON(t, "GET", base+"/role_bindings?orderBy=role_id&search="+url.QueryEscape("scope = 'global'"), bearer, nil)
		var result grantListResponse
		var raw any
		if code != 200 || json.Unmarshal(body, &result) != nil || json.Unmarshal(body, &raw) != nil || schema.VisitJSON(raw) != nil || result.Total != int64(want) || len(result.Items) != want {
			t.Fatal("global list", code, string(body))
		}
		for _, row := range result.Items {
			if row.GatewayID != "" || row.Scope != "global" {
				t.Fatal("global grant shape", row)
			}
		}
		return result.Items
	}
	next := func(w *grantWatch, kind pb.EventType, id string) *pb.RoleBinding {
		t.Helper()
		event, err := w.stream.Recv()
		if err != nil || event.GetType() != kind || event.GetResourceId() != id {
			t.Fatalf("global workflow event: %v %v, want %v %s", event, err, kind, id)
		}
		row := event.GetRoleBinding()
		if row.GetScope() == "global" && (row.GatewayId != nil || row.GetUserId() != user.ID) {
			t.Fatal("global stream scope or identity", row)
		}
		return row
	}
	rows := globals(both, 2)
	ids := map[string]string{}
	creatorRole := discoverRole(t, base, both, "gateway:creator").ID
	adminRole := discoverRole(t, base, both, "platform:admin").ID
	for _, row := range rows {
		ids[row.RoleID] = row.ID
		if row.UserID != user.ID {
			t.Fatal("global grantee")
		}
	}
	next(observer, pb.EventType_EVENT_TYPE_CREATED, ids[creatorRole])
	next(observer, pb.EventType_EVENT_TYPE_CREATED, ids[adminRole])
	// Distinct resource keys can be delivered in either order. Each reader starts
	// at the beginning so one lookup cannot discard the other event.
	readGrantEvent(t, kafkaConsumer(t, config), ids[creatorRole], "", "Create", "rolebinding.created")
	readGrantEvent(t, kafkaConsumer(t, config), ids[adminRole], "", "Create", "rolebinding.created")
	for _, row := range globals(both, 2) {
		if row.ID != ids[row.RoleID] {
			t.Fatal("unchanged claim rewrote grant")
		}
	}
	for _, row := range rows {
		if code, _ := requestJSON(t, "GET", base+"/role_bindings/"+row.ID, both, nil); code != 200 {
			t.Fatal("own global read", code)
		}
		if code, _ := requestJSON(t, "GET", base+"/role_bindings/"+row.ID, token(t, key, "bob", "platform:admin"), nil); code != 404 {
			t.Fatal("global disclosure", code)
		}
		if code, _ := requestJSON(t, "DELETE", base+"/role_bindings/"+row.ID, both, nil); code != 403 {
			t.Fatal("global deletion bypassed issuer", code)
		}
	}
	request, _ := json.Marshal(gateways.GrantRequest{RoleID: creatorRole, UserID: user.ID, Scope: "global"})
	if code, _ := requestJSON(t, "POST", base+"/role_bindings", both, request); code != 403 {
		t.Fatal("global creation bypassed issuer", code)
	}
	// A denied domain request still removes role claims before authorization.
	if code, _ := requestJSON(t, "GET", base+"/gateways/"+ksuid.New().String(), plain, nil); code != 404 {
		t.Fatal("denied lookup", code)
	}
	next(observer, pb.EventType_EVENT_TYPE_DELETED, ids[creatorRole])
	next(observer, pb.EventType_EVENT_TYPE_DELETED, ids[adminRole])
	globals(plain, 0)
	rpc, err := client.ListRoleBindings(call(creator), &pb.ListRoleBindingsRequest{UserId: &user.ID})
	if err != nil || len(rpc.GetItems()) != 1 || rpc.Items[0].GatewayId != nil || rpc.Items[0].GetRoleName() != "gateway:creator" {
		t.Fatal("gRPC global projection", rpc, err)
	}
	renewed := rpc.Items[0].GetMetadata().GetId()
	if renewed == ids[creatorRole] {
		t.Fatal("re-grant restored deleted history")
	}
	next(observer, pb.EventType_EVENT_TYPE_CREATED, renewed)
	request, _ = json.Marshal(f.request("global-role-owner"))
	code, body := requestJSON(t, "POST", base+"/gateways", creator, request)
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &gateway) != nil {
		t.Fatal("create from projected role", code, string(body))
	}
	var ownerID string
	if err := f.db.QueryRow("SELECT id FROM role_bindings WHERE gateway_id=$1 AND user_id=$2", gateway.ID, user.ID).Scan(&ownerID); err != nil {
		t.Fatal(err)
	}
	next(observer, pb.EventType_EVENT_TYPE_CREATED, ownerID)
	privileged := watchGrants(t, client, call(creator))
	replay := map[string]bool{}
	for range 2 {
		event, err := privileged.stream.Recv()
		if err != nil || event.GetType() != pb.EventType_EVENT_TYPE_UPDATED {
			t.Fatal("initial replay", event, err)
		}
		replay[event.GetResourceId()] = true
	}
	if !replay[renewed] || !replay[ownerID] {
		t.Fatal("global and Gateway replay", replay)
	}
	rpc, err = client.ListRoleBindings(call(plain), &pb.ListRoleBindingsRequest{UserId: &user.ID})
	if err != nil || len(rpc.GetItems()) != 1 || rpc.Items[0].GetMetadata().GetId() != ownerID || rpc.Items[0].GetGatewayId() != gateway.ID {
		t.Fatal("gRPC role removal changed owner grant", rpc, err)
	}
	next(observer, pb.EventType_EVENT_TYPE_DELETED, renewed)
	next(privileged, pb.EventType_EVENT_TYPE_DELETED, renewed)
	if code, _ := requestJSON(t, "GET", base+"/gateways/"+gateway.ID, plain, nil); code != 200 {
		t.Fatal("creator removal lost ownership", code)
	}
	if code, _ := requestJSON(t, "POST", base+"/gateways", plain, request); code != 403 {
		t.Fatal("removed creator retained creation", code)
	}
	recipient := currentUser(t, base, token(t, key, "bob"))
	viewer := discoverRole(t, base, plain, "gateway:viewer")
	request, _ = json.Marshal(gateways.GrantRequest{UserID: recipient.ID, RoleID: viewer.ID, GatewayID: gateway.ID, Scope: "gateway"})
	code, body = requestJSON(t, "POST", base+"/role_bindings", plain, request)
	var grant grantResponse
	if code != 201 || json.Unmarshal(body, &grant) != nil {
		t.Fatal("owner sharing after creator removal", code, string(body))
	}
	next(observer, pb.EventType_EVENT_TYPE_CREATED, grant.ID)
	next(privileged, pb.EventType_EVENT_TYPE_CREATED, grant.ID)
	// The existing privileged stream must not restore its old creator claim.
	var count int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE user_id=$1 AND scope='global' AND deleted_at IS NULL", user.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("old stream restored roles", count, err)
	}
	if code, _ := requestJSON(t, "GET", base+"/gateways/"+gateway.ID, token(t, key, "bob"), nil); code != 200 {
		t.Fatal("viewer access", code)
	}
	observer.cancel()
	privileged.cancel()
	// A preparation error blocks the domain call and does not create an identity.
	if _, err := f.db.Exec(`CREATE FUNCTION fail_global() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.scope='global' THEN RAISE EXCEPTION 'global write rejected'; END IF; RETURN NEW; END $$; CREATE TRIGGER fail_global BEFORE INSERT ON role_bindings FOR EACH ROW EXECUTE FUNCTION fail_global()`); err != nil {
		t.Fatal(err)
	}
	failed := token(t, key, "failed-user", "gateway:creator")
	request, _ = json.Marshal(f.request("must-not-create"))
	if code, _ := requestJSON(t, "POST", base+"/gateways", failed, request); code != 500 {
		t.Fatal("preparation became success or auth failure", code)
	}
	if _, err := client.ListRoleBindings(call(failed), &pb.ListRoleBindingsRequest{UserId: &user.ID}); status.Code(err) != codes.Internal {
		t.Fatal("gRPC preparation failure", err)
	}
	if err := f.db.QueryRow("SELECT count(*) FROM users WHERE subject='failed-user'").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed preparation committed identity", err)
	}
	if _, err := f.db.Exec("DROP TRIGGER fail_global ON role_bindings; DROP FUNCTION fail_global()"); err != nil {
		t.Fatal(err)
	}
	stop()
	if err := f.service.PrepareRequest(context.Background(), principal("alice", "gateway:creator")); err != nil {
		t.Fatal(err)
	}
	offline, err := f.service.ListGrants(context.Background(), principal("alice"), gateways.GrantQuery{Page: 1, Size: 100, Search: "scope = 'global'"})
	if err != nil || len(offline.Items) != 1 {
		t.Fatal("offline projection", err)
	}
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	base = address + "/api/hypershell/v1"
	restored := globals(creator, 1)
	if restored[0].ID != offline.Items[0].Grant.ID {
		t.Fatal("restart changed global ID")
	}
	readGrantEvent(t, consumer, restored[0].ID, "", "Create", "rolebinding.created")
	if code, _ := requestJSON(t, "GET", base+"/gateways/"+gateway.ID, plain, nil); code != 200 {
		t.Fatal("restart lost ownership", code)
	}
}

func TestGlobalRoleProjectionIsAtomic(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	p := principal("alice", "gateway:creator")
	if err := f.service.PrepareRequest(ctx, p); err != nil {
		t.Fatal(err)
	}
	before, err := f.service.ListGrants(ctx, p, gateways.GrantQuery{Page: 1, Size: 100})
	if err != nil || len(before.Items) != 1 {
		t.Fatal(err)
	}
	original := before.Items[0].Grant
	duplicate := original
	duplicate.ID = ksuid.New().String()
	if err := f.storage.Create(ctx, "RoleBinding", duplicate); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("duplicate global key", err)
	}
	invalid := original
	invalid.ID = ksuid.New().String()
	invalid.GatewayID = &f.cluster
	if err := f.storage.Create(ctx, "RoleBinding", invalid); err == nil {
		t.Fatal("global grant accepted a Gateway")
	}
	if _, err := f.db.Exec(`CREATE FUNCTION fail_role_event() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'event write rejected'; END $$; CREATE TRIGGER fail_role_event BEFORE INSERT ON stego_outbox.messages FOR EACH ROW EXECUTE FUNCTION fail_role_event()`); err != nil {
		t.Fatal(err)
	}
	changed := principal("alice", "platform:admin")
	changed.Username = "renamed"
	if err := f.service.PrepareRequest(ctx, changed); err == nil {
		t.Fatal("event failure committed projection")
	}
	value, err := f.storage.Get(ctx, "RoleBinding", original.ID)
	if err != nil || value.(model.RoleBinding).DeletedAt.Valid {
		t.Fatal("failed event removed old role", err)
	}
	var username string
	if err := f.db.QueryRow("SELECT username FROM users WHERE id=$1", original.UserID).Scan(&username); err != nil || username != "alice" {
		t.Fatal("failed event changed profile", err)
	}
	var count int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE user_id=$1 AND deleted_at IS NULL", original.UserID).Scan(&count); err != nil || count != 1 {
		t.Fatal("partial global replacement", count, err)
	}
}

func BenchmarkGlobalRolePreparation(b *testing.B) {
	f := database(b)
	ctx := context.Background()
	p := principal("actor", "gateway:creator", "platform:admin")
	if err := f.service.PrepareRequest(ctx, p); err != nil {
		b.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO users(id,created_time,updated_time,username,issuer,subject,email,name)
 SELECT lpad(n::text,27,'0'),now(),now(),'unrelated','https://issuer.example','unrelated-'||n,'',''
 FROM generate_series(1,10000) AS n;
 INSERT INTO role_bindings(id,created_time,updated_time,user_id,role_id,scope)
 SELECT lpad((n+10000)::text,27,'0'),now(),now(),lpad(n::text,27,'0'),r.id,'global'
 FROM generate_series(1,10000) AS n,roles r WHERE r.name='gateway:creator';
 ANALYZE users; ANALYZE role_bindings; ANALYZE roles`); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if err := f.service.PrepareRequest(ctx, p); err != nil {
			b.Fatal(err)
		}
	}
}

func TestGlobalRoleMigrationPreservesGatewayGrants(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	owner := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("migration-owner"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := f.service.ListGrants(ctx, owner, gateways.GrantQuery{Page: 1, Size: 100})
	if err != nil || len(before.Items) != 1 {
		t.Fatal("existing owner grant", err)
	}
	old := before.Items[0].Grant
	migration, err := os.ReadFile("../migrations/000005_global_roles.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.db.ExecContext(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.service.Get(ctx, principal("alice"), gateway.ID); err != nil {
		t.Fatal("migration lost ownership", err)
	}
	value, err := f.storage.Get(ctx, "RoleBinding", old.ID)
	if err != nil {
		t.Fatal(err)
	}
	after := value.(model.RoleBinding)
	if after.GatewayID == nil || *after.GatewayID != gateway.ID || !after.CreatedTime.Equal(old.CreatedTime) || !after.UpdatedTime.Equal(old.UpdatedTime) {
		t.Fatal("migration changed grant", after)
	}
	invalid := after
	invalid.ID = ksuid.New().String()
	invalid.GatewayID = nil
	if err := f.storage.Create(ctx, "RoleBinding", invalid); err == nil {
		t.Fatal("Gateway grant accepted no Gateway")
	}
	if err := f.service.PrepareRequest(ctx, owner); err != nil {
		t.Fatal("new projection after migration", err)
	}
	list, err := f.service.ListGrants(ctx, owner, gateways.GrantQuery{Page: 1, Size: 100})
	if err != nil || len(list.Items) != 2 {
		t.Fatal("migration did not preserve both scopes", err)
	}
}

func TestConcurrentGlobalRoleProjection(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wave := func(p gateways.Principal) {
		t.Helper()
		const workers = 8
		start := make(chan struct{})
		results := make(chan error, workers)
		var pending sync.WaitGroup
		for range workers {
			pending.Add(1)
			go func() {
				defer pending.Done()
				<-start
				var err error
				for range 20 {
					err = f.service.PrepareRequest(ctx, p)
					if !errors.Is(err, storage.ErrConflict) && !errors.Is(err, storage.ErrSerialization) {
						break
					}
				}
				results <- err
			}()
		}
		close(start)
		pending.Wait()
		close(results)
		for err := range results {
			if err != nil {
				t.Fatal("concurrent projection", err)
			}
		}
	}
	wave(principal("concurrent", "gateway:creator", "platform:admin"))
	if count(t, f.db, "users") != 1 || count(t, f.db, "role_bindings") != 2 || count(t, f.db, "stego_outbox.messages") != 2 {
		t.Fatal("concurrent requests duplicated creation")
	}
	wave(principal("concurrent"))
	var live int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE deleted_at IS NULL").Scan(&live); err != nil || live != 0 {
		t.Fatal("concurrent removal retained roles", err)
	}
	if count(t, f.db, "role_bindings") != 2 || count(t, f.db, "stego_outbox.messages") != 4 {
		t.Fatal("concurrent requests duplicated deletion")
	}
}
