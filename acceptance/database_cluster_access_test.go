package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databaseplacement"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestDatabaseClusterAccessRulesThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	if _, err := f.db.Exec(`CREATE TABLE database_scope_events(kind text NOT NULL);
 CREATE FUNCTION audit_database_scope() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO database_scope_events VALUES (NEW.kind); RETURN NEW; END $$;
 CREATE TRIGGER audit_database_scope AFTER INSERT ON stego_outbox.messages FOR EACH ROW WHEN (NEW.kind LIKE 'manageddatabase.%') EXECUTE FUNCTION audit_database_scope()`); err != nil {
		t.Fatal(err)
	}
	second := ksuid.New().String()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := f.storage.Create(ctx, "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: second}, Name: "second", Provider: "kubernetes", KubeconfigSecret: "second-ref"}); err != nil {
		t.Fatal(err)
	}
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	dir := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "DATABASE_PROVIDER=deployment", `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["first","second","legacy"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"))
	settings = withControllerWriteGrants(t, settings, databaseWriteGrant("first", f.cluster), databaseWriteGrant("second", second), databaseWriteGrant("legacy", "deployment"))
	settings = withCleanupGrants(t, settings, cleanupGrant("first", "ManagedDatabase", "provider", f.cluster), cleanupGrant("first", "ManagedDatabase", "record", f.cluster), cleanupGrant("second", "ManagedDatabase", "provider", second), cleanupGrant("second", "ManagedDatabase", "record", second), cleanupGrant("legacy", "ManagedDatabase", "provider", ""), cleanupGrant("legacy", "ManagedDatabase", "record", ""))
	binary := buildApplication(t)
	stop, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	gatewayAPI, connection := grpcClient(t, rpcAddress, apiTLS)
	databases := pb.NewManagedDatabaseServiceClient(connection)
	cleanup := control.NewDatabaseCleanupServiceClient(connection)
	tokens := map[string]string{}
	for _, name := range []string{"first", "second", "legacy", "admin"} {
		tokens[name] = token(t, key, name, "platform:admin")
	}
	tokens["owner"] = token(t, key, "owner", "gateway:creator")
	call := func(name string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+tokens[name]))
	}
	create := func(cluster string) *pb.Gateway {
		t.Helper()
		r, err := gatewayAPI.CreateGateway(call("owner"), &pb.CreateGatewayRequest{Name: "scoped", ClusterId: cluster, ReleaseId: f.release})
		if err != nil {
			t.Fatal(err)
		}
		return r.Gateway
	}
	firstGateway, secondGateway := create(f.cluster), create(second)
	firstID, secondID := firstGateway.DatabaseId, secondGateway.DatabaseId
	root := address + "/api/hypershell/v1/managed_databases"
	code, body := requestJSON(t, "POST", root, tokens["admin"], []byte(`{"name":"unassigned","provider":"deployment"}`))
	var unassigned httpapi.ManagedDatabase
	if code != 201 || json.Unmarshal(body, &unassigned) != nil {
		t.Fatal("unassigned setup", code)
	}
	read := func(id string) (int64, string, bool) {
		t.Helper()
		read, err := rpc.WithRetainedResourceRead(call("first"))
		if err != nil {
			t.Fatal(err)
		}
		var h metadata.MD
		_, err = databases.GetManagedDatabase(read, &pb.GetManagedDatabaseRequest{Id: id}, grpc.Header(&h))
		if err != nil {
			t.Fatal(err)
		}
		v, deleted, err := rpc.ObservedResourceState(h)
		if err != nil {
			t.Fatal(err)
		}
		cluster, err := databaseplacement.Read(h)
		if err != nil {
			t.Fatal(err)
		}
		return v, cluster, deleted
	}
	if _, cluster, _ := read(firstID); cluster != f.cluster {
		t.Fatal("first placement changed")
	}
	if _, cluster, _ := read(secondID); cluster != second {
		t.Fatal("second placement changed")
	}
	if _, cluster, _ := read(unassigned.ID); cluster != "" {
		t.Fatal("unassigned placement was inferred")
	}
	state := func(id string) model.ManagedDatabase {
		t.Helper()
		value, err := f.storage.Get(ctx, "ManagedDatabase", id)
		if err != nil {
			t.Fatal(err)
		}
		return value.(model.ManagedDatabase)
	}
	write := func(subject, id string, want codes.Code) {
		t.Helper()
		before := state(id)
		beforeEvents := count(t, f.db, "database_scope_events")
		version, _, _ := read(id)
		write, err := rpc.WithResourceVersion(call(subject), version)
		if err != nil {
			t.Fatal(err)
		}
		_, err = databases.UpdateManagedDatabase(write, &pb.UpdateManagedDatabaseRequest{Id: id, Status: pointer("ready")})
		if status.Code(err) != want {
			t.Fatal("database observation scope", subject, err)
		}
		if want != codes.OK && (!reflect.DeepEqual(before, state(id)) || count(t, f.db, "database_scope_events") != beforeEvents) {
			t.Fatal("denied write changed database")
		}
	}
	for _, row := range []struct{ subject, id string }{{"first", secondID}, {"second", firstID}, {"legacy", firstID}, {"first", unassigned.ID}, {"legacy", unassigned.ID}} {
		write(row.subject, row.id, codes.PermissionDenied)
	}
	write("first", firstID, codes.OK)
	write("second", secondID, codes.OK)
	for _, subject := range []string{"first", "second", "legacy"} {
		if code, _ := requestJSON(t, "PATCH", address+"/api/hypershell/v1/managed_clusters/"+f.cluster, tokens[subject], []byte(`{"kubeconfig_secret":"changed"}`)); code != 403 {
			t.Fatal("controller changed a cluster credential reference", code)
		}
		if code, _ := requestJSON(t, "POST", root, tokens[subject], []byte(`{"name":"bypass","provider":"deployment"}`)); code != 403 {
			t.Fatal("controller created catalog record", code)
		}
		if _, err := databases.CreateManagedDatabase(call(subject), &pb.CreateManagedDatabaseRequest{Name: "bypass", Provider: "deployment"}); status.Code(err) != codes.PermissionDenied {
			t.Fatal("controller gRPC create", err)
		}
	}
	for _, row := range []struct{ subject, id string }{{"first", secondID}, {"second", firstID}, {"legacy", firstID}, {"first", unassigned.ID}} {
		before := state(row.id)
		beforeEvents := count(t, f.db, "database_scope_events")
		if _, err := databases.DeleteManagedDatabase(call(row.subject), &pb.DeleteManagedDatabaseRequest{Id: row.id}); status.Code(err) != codes.PermissionDenied {
			t.Fatal("foreign record deletion", err)
		}
		if code, _ := requestJSON(t, "DELETE", root+"/"+row.id, tokens[row.subject], nil); code != 403 {
			t.Fatal("REST foreign record deletion", code)
		}
		if !reflect.DeepEqual(before, state(row.id)) || count(t, f.db, "database_scope_events") != beforeEvents {
			t.Fatal("denied delete changed database")
		}
	}
	if _, err := databases.DeleteManagedDatabase(call("first"), &pb.DeleteManagedDatabaseRequest{Id: firstID}); status.Code(err) != codes.AlreadyExists {
		t.Fatal("live Gateway did not protect database", err)
	}
	for _, gateway := range []*pb.Gateway{firstGateway, secondGateway} {
		if _, err := gatewayAPI.DeleteGateway(call("owner"), &pb.DeleteGatewayRequest{Id: gateway.Metadata.Id}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := databases.DeleteManagedDatabase(call("first"), &pb.DeleteManagedDatabaseRequest{Id: firstID}); err != nil {
		t.Fatal(err)
	}
	if _, err := databases.DeleteManagedDatabase(call("second"), &pb.DeleteManagedDatabaseRequest{Id: secondID}); err != nil {
		t.Fatal(err)
	}
	observe := func(subject, id string, want codes.Code) {
		t.Helper()
		beforeEvents := count(t, f.db, "database_scope_events")
		version, _, deleted := read(id)
		if !deleted {
			t.Fatal("cleanup needs deleted state")
		}
		write, err := rpc.WithResourceVersion(call(subject), version)
		if err != nil {
			t.Fatal(err)
		}
		_, err = cleanup.ObserveDatabaseCleanup(write, &control.ObserveDatabaseCleanupRequest{Id: id, Owner: "provider", Complete: true})
		if status.Code(err) != want {
			t.Fatal("cleanup scope", err)
		}
		if want != codes.OK {
			after, _, _ := read(id)
			if after != version || count(t, f.db, "database_scope_events") != beforeEvents {
				t.Fatal("denied cleanup changed revision")
			}
		}
	}
	observe("first", secondID, codes.PermissionDenied)
	observe("legacy", firstID, codes.PermissionDenied)
	summary := func(subject, cluster string, pending int64, want codes.Code) {
		t.Helper()
		r, err := cleanup.GetDatabaseCleanupSummary(call(subject), &control.GetDatabaseCleanupSummaryRequest{Owner: "provider", Provider: "deployment", ClusterId: cluster})
		if status.Code(err) != want {
			t.Fatal("summary scope", err)
		}
		if want == codes.OK && (r.Pending != pending || r.Target != cluster) {
			t.Fatal("summary included a different cluster")
		}
	}
	summary("first", f.cluster, 1, codes.OK)
	summary("second", second, 1, codes.OK)
	summary("first", second, 0, codes.PermissionDenied)
	summary("legacy", f.cluster, 0, codes.PermissionDenied)
	summary("legacy", "", 0, codes.InvalidArgument)
	observe("first", firstID, codes.OK)
	summary("first", f.cluster, 0, codes.OK)
	summary("second", second, 1, codes.OK)
	connection.Close()
	stop()
	stop, _, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, rpcAddress, apiTLS)
	databases = pb.NewManagedDatabaseServiceClient(connection)
	cleanup = control.NewDatabaseCleanupServiceClient(connection)
	summary("first", f.cluster, 0, codes.OK)
	summary("second", second, 1, codes.OK)
	observe("first", secondID, codes.PermissionDenied)
	observe("second", secondID, codes.OK)
	t.Log("Recorded cluster grants survived API restart; foreign, unassigned, and unscoped writes were denied")
}
