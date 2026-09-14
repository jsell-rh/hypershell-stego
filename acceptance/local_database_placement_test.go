package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/metadata"
)

func TestLocalDatabaseSelectionAndRollback(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	otherCluster, otherDatabase := ksuid.New().String(), ksuid.New().String()
	if err := f.storage.Create(ctx, "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: otherCluster}, Name: "other", Provider: "kubernetes", KubeconfigSecret: "other-cluster"}); err != nil {
		t.Fatal(err)
	}
	ns, err := catalog.DatabaseNamespace(otherDatabase)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.storage.Create(ctx, "ManagedDatabase", model.ManagedDatabase{Meta: model.Meta{ID: otherDatabase}, Name: "other", Provider: "cnpg", Namespace: ns, ClusterID: &otherCluster}); err != nil {
		t.Fatal(err)
	}
	creator := principal("local-owner", "gateway:creator")
	request := f.request("local")
	row, err := f.service.Create(ctx, creator, request)
	if err != nil || row.DatabaseID != f.database || row.ClusterID != f.cluster {
		t.Fatal("Gateway used a foreign database", err)
	}
	request.Name, request.ClusterID = "other-local", otherCluster
	row, err = f.service.Create(ctx, creator, request)
	if err != nil || row.DatabaseID != otherDatabase {
		t.Fatal("second cluster did not use its local server", err)
	}
	if count(t, f.db, "managed_databases") != 2 {
		t.Fatal("Gateway creation created a database server record")
	}
	if _, err := f.service.Update(ctx, creator, row.ID, gateways.PatchRequest{ClusterID: &f.cluster}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("Gateway move did not report a database conflict", err)
	}
	externalCluster, externalID := ksuid.New().String(), ksuid.New().String()
	if err := f.storage.Create(ctx, "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: externalCluster}, Name: "external-site", Provider: "kubernetes", KubeconfigSecret: "external-site"}); err != nil {
		t.Fatal(err)
	}
	request.Name, request.ClusterID = "missing-local-server", externalCluster
	if _, err := f.service.Create(ctx, creator, request); !errors.Is(err, gateways.ErrInvalid) {
		t.Fatal("Gateway selected a remote server", err)
	}
	ns, _ = catalog.DatabaseNamespace(externalID)
	if err := f.storage.Create(ctx, "ManagedDatabase", model.ManagedDatabase{Meta: model.Meta{ID: externalID}, Name: "external-local", Provider: "external", Namespace: ns, ClusterID: &externalCluster}); err != nil {
		t.Fatal(err)
	}
	external, err := gateways.New(f.storage, gateways.Options{DatabaseProvider: gateways.ProviderExternal})
	if err != nil {
		t.Fatal(err)
	}
	request.Name = "external-local"
	if row, err := external.Create(ctx, creator, request); err != nil || row.DatabaseID != externalID {
		t.Fatal("external mode did not select the local registered server", err)
	}
	// A second local server makes selection ambiguous. No partial Gateway writes
	// or events may survive that failure.
	duplicate := ksuid.New().String()
	ns, _ = catalog.DatabaseNamespace(duplicate)
	if err := f.storage.Create(ctx, "ManagedDatabase", model.ManagedDatabase{Meta: model.Meta{ID: duplicate}, Name: "duplicate", Provider: "cnpg", Namespace: ns, ClusterID: &f.cluster}); err != nil {
		t.Fatal(err)
	}
	before := map[string]int{}
	for _, table := range []string{"gateways", "users", "role_bindings", "managed_databases", "stego_outbox.messages"} {
		before[table] = count(t, f.db, table)
	}
	request = f.request("ambiguous")
	if _, err := f.service.Create(ctx, principal("new-local-owner", "gateway:creator"), request); !errors.Is(err, gateways.ErrInvalid) {
		t.Fatal("ambiguous local placement accepted", err)
	}
	for table, total := range before {
		if count(t, f.db, table) != total {
			t.Fatal("failed local placement wrote", table)
		}
	}
}

func TestLocalDatabaseRegistrationThroughRESTAndGRPC(t *testing.T) {
	f := database(t)
	_, brokerConfig := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	dir := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"))
	binary := buildApplication(t)
	stop, address, rpcAddress := startBoth(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stop() }()
	admin := token(t, key, "local-operator", "platform:admin")
	outsider := token(t, key, "local-outsider")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	_, connection := grpcClient(t, rpcAddress, tlsIdentity)
	databases := pb.NewManagedDatabaseServiceClient(connection)
	call := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+admin))
	// A registration with no placement or with the removed provider is invalid.
	base := address + "/api/hypershell/v1/managed_databases"
	for _, input := range []map[string]string{
		{"name": "no-placement", "provider": "cnpg"},
		{"name": "removed", "provider": "deployment", "cluster_id": f.cluster},
		{"name": "missing-cluster", "provider": "external", "cluster_id": ksuid.New().String()},
	} {
		body, _ := json.Marshal(input)
		if code, _ := requestJSON(t, "POST", base, admin, body); code != 400 {
			t.Fatal("invalid database registration accepted", code)
		}
	}
	body, _ := json.Marshal(map[string]string{"name": "local-external", "provider": "external", "cluster_id": f.cluster, "connection_secret": "external-provisioner"})
	if code, _ := requestJSON(t, "POST", base, outsider, body); code != 403 {
		t.Fatal("unprivileged caller registered a database", code)
	}
	code, data := requestJSON(t, "POST", base, admin, body)
	var row httpapi.ManagedDatabase
	if code != 201 || json.Unmarshal(data, &row) != nil || row.ClusterID == nil || *row.ClusterID != f.cluster {
		t.Fatal("REST did not retain database locality", code)
	}
	got, err := databases.GetManagedDatabase(call, &pb.GetManagedDatabaseRequest{Id: row.ID})
	if err != nil || got.GetManagedDatabase().GetClusterId() != f.cluster || got.GetManagedDatabase().GetProvider() != "external" {
		t.Fatal("gRPC did not return registered locality", err)
	}
	created, err := databases.CreateManagedDatabase(call, &pb.CreateManagedDatabaseRequest{Name: "local-cnpg", Provider: "cnpg", ClusterId: f.cluster})
	if err != nil || created.GetManagedDatabase().GetClusterId() != f.cluster {
		t.Fatal("gRPC registration failed", err)
	}
	other := ksuid.New().String()
	patch, _ := json.Marshal(map[string]string{"cluster_id": other})
	if code, _ := requestJSON(t, "PATCH", base+"/"+row.ID, admin, patch); code != 400 {
		t.Fatal("registered locality changed", code)
	}
	stop()
	stop, address, _ = startBoth(t, binary, f.dsn, brokerConfig, settings...)
	code, data = requestJSON(t, "GET", address+"/api/hypershell/v1/managed_databases/"+row.ID, admin, nil)
	if code != 200 || json.Unmarshal(data, &row) != nil || row.ClusterID == nil || *row.ClusterID != f.cluster {
		t.Fatal("restart lost database locality", code)
	}
}

func TestLocalDatabaseMigrationPreservesLegacyRows(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	// Model an old installation without rewriting or deleting its data.
	if _, err := f.db.Exec(`ALTER TABLE managed_databases DROP CONSTRAINT chk_managed_databases_provider; ALTER TABLE managed_databases DROP CONSTRAINT hypershell_database_locality`); err != nil {
		t.Fatal(err)
	}
	legacy := ksuid.New().String()
	ns, _ := catalog.DatabaseNamespace(legacy)
	if _, err := f.db.Exec(`INSERT INTO managed_databases(id,name,provider,namespace,created_time,updated_time) VALUES($1,'legacy','deployment',$2,now(),now())`, legacy, ns); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		for _, name := range []string{"000011_local_database_providers.sql", "000010_database_provider_placement.sql"} {
			data, err := os.ReadFile("../migrations/" + name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.ExecContext(ctx, string(data)); err != nil {
				t.Fatal("locality migration failed", err)
			}
		}
	}
	var provider string
	if err := f.db.QueryRow(`SELECT provider FROM managed_databases WHERE id=$1 AND cluster_id IS NULL`, legacy).Scan(&provider); err != nil || provider != "deployment" {
		t.Fatal("migration changed legacy data", err)
	}
	for _, provider := range []string{"deployment", "cnpg", "external"} {
		id := ksuid.New().String()
		ns, _ := catalog.DatabaseNamespace(id)
		if _, err := f.db.Exec(`INSERT INTO managed_databases(id,name,provider,namespace,created_time,updated_time) VALUES($1,'unplaced',$2,$3,now(),now())`, id, provider, ns); err == nil {
			t.Fatal("unplaced or removed provider passed the database constraint")
		}
	}
}
