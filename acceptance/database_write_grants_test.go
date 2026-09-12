package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestDatabaseControllerWriteGrantsAcrossProvidersAndRestart(t *testing.T) {
	f := database(t)
	assignTestDatabaseCluster(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if _, err := f.db.Exec(`CREATE TABLE database_event_audit(kind text NOT NULL);
CREATE FUNCTION audit_database_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO database_event_audit VALUES (NEW.kind); RETURN NEW; END $$;
CREATE TRIGGER audit_database_event AFTER INSERT ON stego_outbox.messages
FOR EACH ROW WHEN (NEW.kind LIKE 'manageddatabase.%') EXECUTE FUNCTION audit_database_event()`); err != nil {
		t.Fatal(err)
	}
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["provider","other-provider","identity","ungranted","cleanup","wrong-operation"]`)
	settings = withCleanupGrants(t, settings, cleanupGrant("cleanup", "ManagedDatabase", "provider", f.cluster))
	settings = withControllerWriteGrants(t, settings, databaseWriteGrant("provider", f.cluster), databaseWriteGrant("other-provider", "cnpg"), databaseWriteGrant("ordinary", f.cluster), writeGrant("identity", "configure.identity", ""), cleanupGrant("wrong-operation", "ManagedDatabase", "provider", "deployment"))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	_, connection := grpcClient(t, grpcAddress, tlsIdentity)
	client := pb.NewManagedDatabaseServiceClient(connection)
	admin := token(t, key, "admin", "platform:admin")
	create := func(provider string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"name": provider, "provider": provider})
		code, data := requestJSON(t, "POST", address+"/api/hypershell/v1/managed_databases", admin, body)
		var row httpapi.ManagedDatabase
		if code != 201 || json.Unmarshal(data, &row) != nil {
			t.Fatal("create database", code)
		}
		readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Create", "manageddatabase.created")
		awaitQueueEmpty(t, f)
		return row.ID
	}
	id, otherID := create("deployment"), create("cnpg")
	state := func(id string) model.ManagedDatabase {
		t.Helper()
		value, err := f.storage.Get(ctx, "ManagedDatabase", id)
		if err != nil {
			t.Fatal(err)
		}
		return value.(model.ManagedDatabase)
	}
	write := func(id, bearer string, patch *pb.UpdateManagedDatabaseRequest, want codes.Code) {
		t.Helper()
		before := state(id)
		beforeEvents := count(t, f.db, "database_event_audit")
		call, err := rpc.WithResourceVersion(metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer)), before.ResourceVersion)
		if err != nil {
			t.Fatal(err)
		}
		patch.Id = id
		_, err = client.UpdateManagedDatabase(call, patch)
		if status.Code(err) != want {
			t.Fatalf("database observation: %v, want %v", err, want)
		}
		after := state(id)
		if want != codes.OK {
			if !reflect.DeepEqual(before, after) || count(t, f.db, "database_event_audit") != beforeEvents {
				t.Fatal("denied observation changed database state or events")
			}
			return
		}
		if after.ResourceVersion != before.ResourceVersion+1 || count(t, f.db, "database_event_audit") != beforeEvents+1 {
			t.Fatal("observation did not commit one revision and event")
		}
		if patch.Status != nil && !reflect.DeepEqual(after.Status, patch.Status) || patch.ConnectionSecret != nil && !reflect.DeepEqual(after.ConnectionSecret, patch.ConnectionSecret) {
			t.Fatal("observation fields were not stored")
		}
		readCatalogEvent(t, consumer, id, "ManagedDatabases", "Update", "manageddatabase.updated")
		awaitQueueEmpty(t, f)
	}
	observation := func() *pb.UpdateManagedDatabaseRequest {
		return &pb.UpdateManagedDatabaseRequest{Status: pointer("ready"), ConnectionSecret: pointer("database-credentials")}
	}
	for _, subject := range []string{"identity", "other-provider", "ungranted", "ordinary", "cleanup", "wrong-operation"} {
		write(id, token(t, key, subject, "platform:admin"), observation(), codes.PermissionDenied)
	}
	providerToken := token(t, key, "provider", "platform:admin")
	write(otherID, providerToken, observation(), codes.PermissionDenied)
	write(id, providerToken, &pb.UpdateManagedDatabaseRequest{Name: pointer("changed")}, codes.PermissionDenied)
	write(id, providerToken, &pb.UpdateManagedDatabaseRequest{}, codes.PermissionDenied)
	write(id, providerToken, &pb.UpdateManagedDatabaseRequest{Status: pointer("ready"), EngineVersion: pointer("18.1")}, codes.InvalidArgument)
	write(id, providerToken, &pb.UpdateManagedDatabaseRequest{Status: pointer("ready"), Provider: pointer("cnpg")}, codes.InvalidArgument)
	write(id, providerToken, &pb.UpdateManagedDatabaseRequest{Status: pointer("provisioning")}, codes.OK)
	write(id, providerToken, &pb.UpdateManagedDatabaseRequest{ConnectionSecret: pointer("database-credentials")}, codes.OK)
	write(id, providerToken, observation(), codes.OK)
	write(otherID, token(t, key, "other-provider"), observation(), codes.OK)
	before := state(id)
	beforeEvents := count(t, f.db, "database_event_audit")
	if code, _ := requestJSON(t, "PATCH", address+"/api/hypershell/v1/managed_databases/"+id, providerToken, []byte(`{"status":"ready"}`)); code != 428 {
		t.Fatal("REST controller bypass", code)
	}
	if !reflect.DeepEqual(before, state(id)) || count(t, f.db, "database_event_audit") != beforeEvents {
		t.Fatal("REST denial changed database state or events")
	}
	connection.Close()
	stop()
	settings = withControllerWriteGrants(t, settings, databaseWriteGrant("other-provider", "cnpg"))
	stop, _, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	client = pb.NewManagedDatabaseServiceClient(connection)
	write(id, providerToken, observation(), codes.PermissionDenied)
	write(otherID, token(t, key, "other-provider"), &pb.UpdateManagedDatabaseRequest{Status: pointer("provisioning")}, codes.OK)
}
