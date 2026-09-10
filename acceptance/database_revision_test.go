package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestDatabaseRejectsOldObservationAcrossRESTGRPCAndRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	settings = withControllerWriteGrants(t, settings, databaseWriteGrant("controller", "deployment"))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	_, connection := grpcClient(t, grpcAddress, tlsIdentity)
	client := pb.NewManagedDatabaseServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin := token(t, key, "admin", "platform:admin")
	controllerToken := token(t, key, "controller")
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	controller := call(controllerToken)
	root := address + "/api/hypershell/v1/managed_databases"
	code, data := requestJSON(t, "POST", root, admin, []byte(`{"name":"before","provider":"deployment"}`))
	var row httpapi.ManagedDatabase
	if code != 201 || json.Unmarshal(data, &row) != nil {
		t.Fatalf("create: %d %s", code, data)
	}
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Create", "manageddatabase.created")
	awaitQueueEmpty(t, f)
	read := func() (int64, *pb.ManagedDatabase) {
		t.Helper()
		var header metadata.MD
		result, err := client.GetManagedDatabase(controller, &pb.GetManagedDatabaseRequest{Id: row.ID}, grpc.Header(&header))
		if err != nil {
			t.Fatal(err)
		}
		revision, err := rpc.ObservedResourceVersion(header)
		if err != nil {
			t.Fatal(err)
		}
		return revision, result.ManagedDatabase
	}
	versioned := func(parent context.Context, revision int64) context.Context {
		t.Helper()
		result, err := rpc.WithResourceVersion(parent, revision)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	original, _ := read()
	if original != 1 {
		t.Fatal("new database has no initial revision", original)
	}
	code, data = requestJSON(t, "PATCH", root+"/"+row.ID, admin, []byte(`{"name":"changed"}`))
	if code != 200 {
		t.Fatalf("desired update: %d %s", code, data)
	}
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Update", "manageddatabase.updated")
	awaitQueueEmpty(t, f)
	current, changed := read()
	if current != original+1 || changed.Name != "changed" {
		t.Fatal("desired change did not advance revision")
	}
	update := &pb.UpdateManagedDatabaseRequest{Id: row.ID, Status: pointer("ready"), ConnectionSecret: pointer("database-credentials")}
	reject := func(parent context.Context, want codes.Code) {
		t.Helper()
		if _, err := client.UpdateManagedDatabase(parent, update); status.Code(err) != want {
			t.Fatalf("observation: %v; want %v", err, want)
		}
	}
	reject(versioned(controller, original), codes.Aborted)
	reject(controller, codes.FailedPrecondition)
	reject(versioned(call(admin), current), codes.PermissionDenied)
	for _, values := range [][]string{{"0"}, {"01"}, {"-1"}, {"1", "1"}, {"9223372036854775808"}} {
		md, _ := metadata.FromOutgoingContext(controller)
		md = md.Copy()
		md.Set("if-resource-version", values...)
		reject(metadata.NewOutgoingContext(ctx, md), codes.InvalidArgument)
	}
	if code, _ := requestJSON(t, "PATCH", root+"/"+row.ID, controllerToken, []byte(`{"status":"ready"}`)); code != 428 {
		t.Fatal("REST bypassed the revision requirement", code)
	}
	var deniedHeader metadata.MD
	if _, err := client.GetManagedDatabase(call(token(t, key, "outsider")), &pb.GetManagedDatabaseRequest{Id: row.ID}, grpc.Header(&deniedHeader)); status.Code(err) != codes.PermissionDenied || len(deniedHeader.Get("resource-version")) != 0 {
		t.Fatal("denied read exposed a revision", err)
	}
	assertUnchanged := func() {
		t.Helper()
		revision, value := read()
		var queued int
		if err := f.db.QueryRow(`SELECT count(*) FROM stego_outbox.messages WHERE kind LIKE 'manageddatabase.%'`).Scan(&queued); err != nil {
			t.Fatal(err)
		}
		if revision != current || value.GetStatus() == "ready" || value.ConnectionSecret != nil || queued != 0 {
			t.Fatal("rejected result changed database state or its events")
		}
	}
	assertUnchanged()
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_database_observation CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	reject(versioned(controller, current), codes.Internal)
	assertUnchanged()
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_database_observation`); err != nil {
		t.Fatal(err)
	}
	stop()
	connection.Close()
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	client = pb.NewManagedDatabaseServiceClient(connection)
	if revision, _ := read(); revision != current {
		t.Fatal("restart changed the revision")
	}
	reject(versioned(controller, original), codes.Aborted)
	result, err := client.UpdateManagedDatabase(versioned(controller, current), update)
	if err != nil || result.ManagedDatabase.GetStatus() != "ready" || result.ManagedDatabase.GetConnectionSecret() != "database-credentials" {
		t.Fatal("fresh observation failed", result, err)
	}
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Update", "manageddatabase.updated")
	awaitQueueEmpty(t, f)
	final, _ := read()
	if final != current+1 {
		t.Fatal("observation did not advance revision")
	}
	reject(versioned(controller, current), codes.Aborted)
	root = address + "/api/hypershell/v1/managed_databases"
	code, data = requestJSON(t, "GET", root+"/"+row.ID, admin, nil)
	if code != 200 || json.Unmarshal(data, &row) != nil || row.Name != "changed" || row.Status == nil || *row.Status != "ready" {
		t.Fatalf("REST observation: %d %s", code, data)
	}
	if code, _ := requestJSON(t, "DELETE", root+"/"+row.ID, admin, nil); code != 204 {
		t.Fatal("delete", code)
	}
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Delete", "manageddatabase.deleted")
	awaitQueueEmpty(t, f)
	reject(versioned(controller, final), codes.NotFound)
	var retained int64
	if err := f.db.QueryRow("SELECT stego_revision FROM managed_databases WHERE id=$1 AND deleted_at IS NOT NULL", row.ID).Scan(&retained); err != nil || retained != final+1 {
		t.Fatal("deletion lost version history", retained, err)
	}
}
