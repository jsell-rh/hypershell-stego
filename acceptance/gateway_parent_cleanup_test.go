package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestClusterDeletionWaitsForGatewaySQLAndWorkloadCleanupAcrossRestart(t *testing.T) {
	f := database(t)
	if _, err := f.db.Exec(`CREATE TABLE parent_delete_events(kind text NOT NULL);
CREATE FUNCTION public.audit_parent_delete() RETURNS trigger LANGUAGE plpgsql
SECURITY DEFINER SET search_path = pg_catalog, pg_temp AS $$
BEGIN INSERT INTO public.parent_delete_events VALUES (NEW.kind); RETURN NEW; END $$;
REVOKE ALL ON FUNCTION public.audit_parent_delete() FROM PUBLIC;
CREATE TRIGGER audit_parent_delete AFTER INSERT ON stego_outbox.messages
FOR EACH ROW WHEN (NEW.kind LIKE 'managed%.deleted') EXECUTE FUNCTION audit_parent_delete()`); err != nil {
		t.Fatal(err)
	}
	requireFixtureRuntimePermissionDenied(t, f, "INSERT INTO public.parent_delete_events(kind) VALUES ('direct-runtime-write')")
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	dir := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["worker"]`)
	settings = withCleanupGrants(t, settings, cleanupGrant("worker", "Gateway", "allocation", f.cluster), cleanupGrant("worker", "Gateway", "workload", f.cluster), cleanupGrant("worker", "Gateway", "sql", f.cluster))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	// Permit four bounded lease recovery windows and the API checks.
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	admin := token(t, key, "operator", "platform:admin")
	owner := token(t, key, "owner", "gateway:creator")
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	_, connection := grpcClient(t, grpcAddress, apiTLS)
	state := control.NewGatewayIdentityServiceClient(connection)
	restart := func() {
		connection.Close()
		stop()
		stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
		_, connection = grpcClient(t, grpcAddress, apiTLS)
		state = control.NewGatewayIdentityServiceClient(connection)
		// A claim can commit before the stopped process receives its receipt.
		// Require recovery within the generated lease and delivery bounds.
		awaitQueueEmptyAfterRestart(t, f)
	}
	var ids []string
	for _, name := range []string{"first", "second"} {
		body, _ := json.Marshal(f.request(name))
		code, data := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, body)
		var row gatewayResponse
		if code != 201 || json.Unmarshal(data, &row) != nil {
			t.Fatal("create Gateway", code)
		}
		ids = append(ids, row.ID)
		readGatewayEvent(t, consumer, row.ID, "Create", "gateway.created")
		if code, _ := requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+row.ID, owner, nil); code != 202 {
			t.Fatal("delete Gateway", code)
		}
		readGatewayEvent(t, consumer, row.ID, "Update", "gateway.updated")
	}
	awaitQueueEmpty(t, f)
	blocked := func(resource, id, table string) {
		t.Helper()
		var before, after string
		if err := f.db.QueryRow("SELECT to_jsonb(parent)::text FROM "+table+" parent WHERE id=$1 AND deleted_at IS NULL", id).Scan(&before); err != nil {
			t.Fatal(err)
		}
		events := count(t, f.db, "parent_delete_events")
		code, _ := requestJSON(t, "DELETE", address+"/api/hypershell/v1/"+resource+"/"+id, admin, nil)
		if code != 409 {
			t.Fatalf("parent deletion bypassed pending cleanup: %s got %d, want 409", resource, code)
		}
		_, err := pb.NewManagedClusterServiceClient(connection).DeleteManagedCluster(call(admin), &pb.DeleteManagedClusterRequest{Id: id})
		if status.Code(err) != codes.AlreadyExists {
			t.Fatal("gRPC parent deletion bypassed pending cleanup", err)
		}
		if err := f.db.QueryRow("SELECT to_jsonb(parent)::text FROM "+table+" parent WHERE id=$1 AND deleted_at IS NULL", id).Scan(&after); err != nil || before != after || count(t, f.db, "parent_delete_events") != events {
			t.Fatal("denied parent deletion changed state or events", err)
		}
	}
	blocked("managed_clusters", f.cluster, "managed_clusters")
	restart()
	blocked("managed_clusters", f.cluster, "managed_clusters")
	observe := func(id, owner string, complete bool) {
		t.Helper()
		current, err := state.GetGatewayIdentityState(call(token(t, key, "worker")), &control.GetGatewayIdentityStateRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		write, err := rpc.WithResourceVersion(call(token(t, key, "worker")), current.ResourceVersion)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := state.ObserveGatewayCleanup(write, &control.ObserveGatewayCleanupRequest{Id: id, Owner: owner, Target: f.cluster, Complete: complete}); err != nil {
			t.Fatal(err)
		}
		readGatewayEvent(t, consumer, id, "Update", "gateway.updated")
		awaitQueueEmpty(t, f)
	}
	// SQL and workload cleanup each block cluster deletion. A late workload
	// effect must not discard a completed SQL observation.
	observe(ids[0], "sql", true)
	observe(ids[1], "workload", true)
	blocked("managed_clusters", f.cluster, "managed_clusters")
	restart()
	blocked("managed_clusters", f.cluster, "managed_clusters")
	observe(ids[0], "workload", true)
	blocked("managed_clusters", f.cluster, "managed_clusters")
	observe(ids[0], "workload", false)
	observe(ids[1], "sql", true)
	blocked("managed_clusters", f.cluster, "managed_clusters")
	restart()
	blocked("managed_clusters", f.cluster, "managed_clusters")
	observe(ids[0], "workload", true)
	for _, id := range ids {
		current, err := state.GetGatewayIdentityState(call(token(t, key, "worker")), &control.GetGatewayIdentityStateRequest{Id: id})
		if err != nil || !current.GetCleanupTargets()["sql"].GetTargets()[f.cluster] {
			t.Fatal("workload retry lost SQL completion", err)
		}
	}
	// Retained state allocations keep the cluster available for cleanup.
	blocked("managed_clusters", f.cluster, "managed_clusters")
	observe(ids[0], "allocation", true)
	restart()
	blocked("managed_clusters", f.cluster, "managed_clusters")
	observe(ids[1], "allocation", true)
	if code, _ := requestJSON(t, "DELETE", address+"/api/hypershell/v1/managed_clusters/"+f.cluster, admin, nil); code != 204 {
		t.Fatal("complete Gateway cleanup did not release cluster", code)
	}
	if count(t, f.db, "parent_delete_events") != 1 {
		t.Fatal("cluster deletion did not commit one event")
	}
	if code, _ := requestJSON(t, "GET", address+"/api/hypershell/v1/managed_clusters/"+f.cluster, admin, nil); code != 404 {
		t.Fatal("deleted cluster remained public", code)
	}
}
