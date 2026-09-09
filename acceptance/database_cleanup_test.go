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
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestDatabaseCleanupObservationIsAtomicAndSurvivesRestart(t *testing.T) {
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
	databases := pb.NewManagedDatabaseServiceClient(connection)
	cleanup := control.NewDatabaseCleanupServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin := token(t, key, "admin", "platform:admin")
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	controller := call(token(t, key, "controller"))
	root := address + "/api/hypershell/v1/managed_databases"
	code, data := requestJSON(t, "POST", root, admin, []byte(`{"name":"cleanup","provider":"deployment"}`))
	var row httpapi.ManagedDatabase
	if code != 201 || json.Unmarshal(data, &row) != nil {
		t.Fatalf("create: %d %s", code, data)
	}
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Create", "manageddatabase.created")
	awaitQueueEmpty(t, f)
	read := func(wantDeleted, wantComplete bool, wantVersion int64) {
		t.Helper()
		retained, err := rpc.WithRetainedResourceRead(controller)
		if err != nil {
			t.Fatal(err)
		}
		var header metadata.MD
		response, err := databases.GetManagedDatabase(retained, &pb.GetManagedDatabaseRequest{Id: row.ID}, grpc.Header(&header))
		if err != nil {
			t.Fatal(err)
		}
		revision, deleted, err := rpc.ObservedResourceState(header)
		if err != nil {
			t.Fatal(err)
		}
		states, err := rpc.ObservedCleanupObservations(header)
		complete, declared := states["provider"]
		if err != nil || revision != wantVersion || deleted != wantDeleted || !declared || complete != wantComplete || response.ManagedDatabase.GetMetadata().GetId() != row.ID {
			t.Fatal("invalid cleanup state", revision, deleted, states, err)
		}
	}
	observe := func(parent context.Context, version int64, owner string, complete bool, want codes.Code) {
		t.Helper()
		if version > 0 {
			var err error
			parent, err = rpc.WithResourceVersion(parent, version)
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err := cleanup.ObserveDatabaseCleanup(parent, &control.ObserveDatabaseCleanupRequest{Id: row.ID, Owner: owner, Complete: complete})
		if status.Code(err) != want {
			t.Fatal("cleanup result", err, "want", want)
		}
	}
	read(false, false, 1)
	observe(controller, 1, "provider", true, codes.Aborted)
	if code, _ := requestJSON(t, "DELETE", root+"/"+row.ID, admin, nil); code != 204 {
		t.Fatal("delete", code)
	}
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Delete", "manageddatabase.deleted")
	awaitQueueEmpty(t, f)
	read(true, false, 2)
	observe(call(admin), 2, "provider", true, codes.PermissionDenied)
	observe(call(token(t, key, "outsider")), 2, "provider", true, codes.PermissionDenied)
	observe(controller, 2, "other", true, codes.PermissionDenied)
	observe(controller, 0, "provider", true, codes.FailedPrecondition)
	observe(metadata.AppendToOutgoingContext(controller, "if-resource-version", "01"), 0, "provider", true, codes.InvalidArgument)
	observe(controller, 1, "provider", true, codes.Aborted)
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_cleanup_event CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	observe(controller, 2, "provider", true, codes.Internal)
	read(true, false, 2)
	var queued int
	if err := f.db.QueryRow("SELECT count(*) FROM stego_outbox.messages").Scan(&queued); err != nil || queued != 0 {
		t.Fatal("rejected cleanup committed an event", queued, err)
	}
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_cleanup_event"); err != nil {
		t.Fatal(err)
	}
	observe(controller, 2, "provider", true, codes.OK)
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Delete", "manageddatabase.deleted")
	awaitQueueEmpty(t, f)
	read(true, true, 3)
	stop()
	connection.Close()
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	databases = pb.NewManagedDatabaseServiceClient(connection)
	cleanup = control.NewDatabaseCleanupServiceClient(connection)
	read(true, true, 3)
	observe(controller, 2, "provider", false, codes.Aborted)
	observe(controller, 3, "provider", false, codes.OK)
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Delete", "manageddatabase.deleted")
	awaitQueueEmpty(t, f)
	read(true, false, 4)
	observe(controller, 4, "provider", true, codes.OK)
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Delete", "manageddatabase.deleted")
	awaitQueueEmpty(t, f)
	read(true, true, 5)
	// Retained input changes require a fresh absence observation.
	if _, err := f.db.Exec("UPDATE managed_databases SET name='changed-cleanup-input' WHERE id=$1", row.ID); err != nil {
		t.Fatal(err)
	}
	read(true, false, 6)
	observe(controller, 5, "provider", true, codes.Aborted)
	if code, _ := requestJSON(t, "GET", address+"/api/hypershell/v1/managed_databases/"+row.ID, admin, nil); code != 404 {
		t.Fatal("cleanup changed public deletion visibility", code)
	}
}
