package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGatewayCleanupKeepsBothClusterTargetsAfterRestart(t *testing.T) {
	f := database(t)
	if _, err := f.db.Exec(`CREATE TABLE target_cleanup_events(kind text NOT NULL);
CREATE FUNCTION audit_target_cleanup() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO target_cleanup_events VALUES (NEW.kind); RETURN NEW; END $$;
CREATE TRIGGER audit_target_cleanup AFTER INSERT ON stego_outbox.messages
FOR EACH ROW WHEN (NEW.kind LIKE 'gateway.%' OR NEW.kind='managedcluster.deleted')
EXECUTE FUNCTION audit_target_cleanup()`); err != nil {
		t.Fatal(err)
	}
	second := ksuid.New().String()
	unrecorded := ksuid.New().String()
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	// New Gateways use local CNPG placement. The fixture below restores an old move.
	settings = append(settings, "DATABASE_PROVIDER=cnpg", "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller","other-controller","identity-controller","ungranted"]`)
	settings = withCleanupGrants(t, settings, cleanupGrant("identity-controller", "Gateway", "identity", ""), cleanupGrant("controller", "Gateway", "workload", f.cluster), cleanupGrant("controller", "Gateway", "workload", unrecorded), cleanupGrant("other-controller", "Gateway", "workload", second))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	_, connection := grpcClient(t, grpcAddress, tlsIdentity)
	cleanup := control.NewGatewayIdentityServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin := token(t, key, "admin", "platform:admin")
	owner := token(t, key, "alice", "gateway:creator")
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	controller := call(token(t, key, "controller", "platform:admin"))
	otherController := call(token(t, key, "other-controller"))
	identityController := call(token(t, key, "identity-controller"))
	ungranted := call(token(t, key, "ungranted"))
	root := address + "/api/hypershell/v1/gateways"
	body, _ := json.Marshal(map[string]string{"name": "cleanup", "cluster_id": f.cluster, "release_id": f.release, "database_id": f.database})
	code, data := requestJSON(t, "POST", root, owner, body)
	var row httpapi.Gateway
	if code != 201 || json.Unmarshal(data, &row) != nil {
		t.Fatalf("create: %d %s", code, data)
	}
	readEvent(t, consumer, row.ID)
	awaitQueueEmpty(t, f)

	if err := f.storage.Create(ctx, "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: second}, Name: "second", Provider: "kubernetes", KubeconfigSecret: "unused"}); err != nil {
		t.Fatal(err)
	}
	patch, _ := json.Marshal(map[string]string{"cluster_id": second})
	beforeEvents := count(t, f.db, "target_cleanup_events")
	if code, _ := requestJSON(t, "PATCH", root+"/"+row.ID, owner, patch); code != 409 {
		t.Fatal("Gateway move bypassed database locality", code)
	}
	current, err := cleanup.GetGatewayIdentityState(controller, &control.GetGatewayIdentityStateRequest{Id: row.ID})
	if err != nil || current.GetResourceVersion() != 1 || count(t, f.db, "target_cleanup_events") != beforeEvents {
		t.Fatal("denied move changed Gateway state or events", current.GetResourceVersion(), beforeEvents, count(t, f.db, "target_cleanup_events"), err)
	}
	// Restore data from an old installation while the API is stopped. A shared,
	// unassigned database must not hide the Gateway's former cluster reference.
	stop()
	connection.Close()
	restoreLegacyGatewayPlacement(t, f, row.ID, second)
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	root = address + "/api/hypershell/v1/gateways"
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	cleanup = control.NewGatewayIdentityServiceClient(connection)
	read := func(version int64, deleted, firstComplete, secondComplete, identityComplete bool) {
		t.Helper()
		state, err := cleanup.GetGatewayIdentityState(controller, &control.GetGatewayIdentityStateRequest{Id: row.ID})
		if err != nil {
			t.Fatal(err)
		}
		targets := state.GetCleanupTargets()["workload"].GetTargets()
		_, firstFound := targets[f.cluster]
		_, secondFound := targets[second]
		if state.ResourceVersion != version || state.Deleted != deleted || len(targets) != 2 || !firstFound || !secondFound || targets[f.cluster] != firstComplete || targets[second] != secondComplete || state.Cleanup["workload"] != (firstComplete && secondComplete) || state.Cleanup["identity"] != identityComplete {
			t.Fatal("invalid target state", state)
		}
	}
	observe := func(parent context.Context, version int64, owner, target string, complete bool, want codes.Code) {
		t.Helper()
		write, err := rpc.WithResourceVersion(parent, version)
		if err != nil {
			t.Fatal(err)
		}
		_, err = cleanup.ObserveGatewayCleanup(write, &control.ObserveGatewayCleanupRequest{Id: row.ID, Owner: owner, Target: target, Complete: complete})
		if status.Code(err) != want {
			t.Fatal("target cleanup result", err, "want", want)
		}
	}
	event := func() {
		t.Helper()
		readGatewayEvent(t, consumer, row.ID, "Delete", "gateway.deleted")
		awaitQueueEmpty(t, f)
	}
	blocked := func(id string) {
		t.Helper()
		var before, after string
		if err := f.db.QueryRow("SELECT to_jsonb(c)::text FROM managed_clusters c WHERE id=$1 AND deleted_at IS NULL", id).Scan(&before); err != nil {
			t.Fatal(err)
		}
		events := count(t, f.db, "target_cleanup_events")
		if code, _ := requestJSON(t, "DELETE", address+"/api/hypershell/v1/managed_clusters/"+id, admin, nil); code != 409 {
			t.Fatal("cluster deletion ignored unfinished retained target", code)
		}
		_, err := pb.NewManagedClusterServiceClient(connection).DeleteManagedCluster(call(admin), &pb.DeleteManagedClusterRequest{Id: id})
		if status.Code(err) != codes.AlreadyExists {
			t.Fatal("gRPC cluster deletion ignored unfinished retained target", err)
		}
		if err := f.db.QueryRow("SELECT to_jsonb(c)::text FROM managed_clusters c WHERE id=$1 AND deleted_at IS NULL", id).Scan(&after); err != nil || before != after || count(t, f.db, "target_cleanup_events") != events {
			t.Fatal("denied cluster deletion changed state or events", err)
		}
	}
	read(2, false, false, false, false)
	blocked(f.cluster)
	blocked(second)
	observe(controller, 2, "workload", f.cluster, true, codes.Aborted)
	if code, _ := requestJSON(t, "DELETE", root+"/"+row.ID, owner, nil); code != 204 {
		t.Fatal("delete", code)
	}
	event()
	read(3, true, false, false, false)
	blocked(f.cluster)
	blocked(second)
	observe(call(admin), 3, "workload", f.cluster, true, codes.PermissionDenied)
	observe(call(token(t, key, "outsider")), 3, "workload", f.cluster, true, codes.PermissionDenied)
	observe(controller, 3, "workload", "", true, codes.PermissionDenied)
	observe(controller, 3, "identity", f.cluster, true, codes.PermissionDenied)
	observe(controller, 3, "workload", ksuid.New().String(), true, codes.PermissionDenied)
	observe(controller, 3, "workload", second, true, codes.PermissionDenied)
	observe(otherController, 3, "workload", f.cluster, true, codes.PermissionDenied)
	observe(controller, 3, "identity", "", true, codes.PermissionDenied)
	observe(identityController, 3, "workload", f.cluster, true, codes.PermissionDenied)
	observe(ungranted, 3, "workload", f.cluster, true, codes.PermissionDenied)
	read(3, true, false, false, false)
	observe(controller, 3, "workload", unrecorded, true, codes.Aborted)
	read(3, true, false, false, false)
	observe(controller, 3, "workload", f.cluster, true, codes.OK)
	event()
	read(4, true, true, false, false)
	observe(otherController, 3, "workload", second, true, codes.Aborted)
	observe(identityController, 4, "identity", "", true, codes.OK)
	event()
	read(5, true, true, false, true)
	stop()
	connection.Close()
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	cleanup = control.NewGatewayIdentityServiceClient(connection)
	read(5, true, true, false, true)
	blocked(second)
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_target CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	observe(otherController, 5, "workload", second, true, codes.Internal)
	read(5, true, true, false, true)
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_target"); err != nil {
		t.Fatal(err)
	}
	observe(otherController, 5, "workload", second, true, codes.OK)
	event()
	read(6, true, true, true, true)
	observe(controller, 6, "workload", f.cluster, false, codes.OK)
	event()
	read(7, true, false, true, true)
	blocked(f.cluster)
	blocked(second)
	stop()
	connection.Close()
	settings = withCleanupGrants(t, settings, cleanupGrant("identity-controller", "Gateway", "identity", ""), cleanupGrant("other-controller", "Gateway", "workload", second))
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	cleanup = control.NewGatewayIdentityServiceClient(connection)
	observe(controller, 7, "workload", f.cluster, true, codes.PermissionDenied)
	read(7, true, false, true, true)
	observe(otherController, 7, "workload", second, false, codes.OK)
	event()
	read(8, true, false, false, true)
	blocked(f.cluster)
	blocked(second)
	if code, _ := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways/"+row.ID, owner, nil); code != 404 {
		t.Fatal("cleanup changed public deletion state", code)
	}
	stop()
	connection.Close()
	settings = withCleanupGrants(t, settings, cleanupGrant("controller", "Gateway", "workload", f.cluster), cleanupGrant("other-controller", "Gateway", "workload", second))
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	cleanup = control.NewGatewayIdentityServiceClient(connection)
	observe(controller, 8, "workload", f.cluster, true, codes.OK)
	event()
	observe(otherController, 9, "workload", second, true, codes.OK)
	event()
	read(10, true, true, true, true)
	for _, id := range []string{f.cluster, second} {
		if code, _ := requestJSON(t, "DELETE", address+"/api/hypershell/v1/managed_clusters/"+id, admin, nil); code != 204 {
			t.Fatal("finished target did not release cluster", code)
		}
		readCatalogEvent(t, consumer, id, "ManagedClusters", "Delete", "managedcluster.deleted")
	}
}
