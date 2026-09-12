package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/databaseplacement"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func TestDeploymentPlacementThroughGeneratedRuntime(t *testing.T) {
	f := databaseSetup(t, false)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`, "DATABASE_PROVIDER=", "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	base := address + "/api/hypershell/v1"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	admin := token(t, key, "operator", "platform:admin")
	alice := token(t, key, "alice", "gateway:creator")
	controller := token(t, key, "controller")
	owner := token(t, key, "alice")
	bob := token(t, key, "bob", "gateway:creator")
	code, body := requestJSON(t, "POST", base+"/managed_clusters", admin, []byte(`{"name":"cluster","provider":"kubernetes","kubeconfig_secret":"cluster-ref"}`))
	var cluster httpapi.ManagedCluster
	if code != 201 || json.Unmarshal(body, &cluster) != nil {
		t.Fatal("cluster setup", code, string(body))
	}
	code, body = requestJSON(t, "POST", base+"/gateway_releases", admin, []byte(`{"name":"release","image":"registry.example/gateway:v1"}`))
	var release httpapi.GatewayRelease
	if code != 201 || json.Unmarshal(body, &release) != nil {
		t.Fatal("release setup", code, string(body))
	}
	if count(t, f.db, "managed_databases") != 0 {
		t.Fatal("deployment workflow has a pre-created database")
	}
	client, connection := grpcClient(t, grpcAddress, tlsIdentity)
	databases := pb.NewManagedDatabaseServiceClient(connection)
	databaseWatch, err := databases.WatchManagedDatabases(call(alice), &pb.WatchManagedDatabasesRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := databaseWatch.Header(); err != nil {
		t.Fatal(err)
	}
	gatewayWatch := watchGateways(t, client, call(owner))
	// A present empty placeholder is valid. An absent or null property is not.
	name := strings.Repeat("g", 255)
	input := gateways.CreateRequest{Name: name, ClusterID: cluster.ID, ReleaseID: release.ID, DatabaseID: ""}
	encoded, _ := json.Marshal(input)
	code, body = requestJSON(t, "POST", base+"/gateways", alice, encoded)
	var first httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &first) != nil {
		t.Fatal("default deployment creation", code, string(body))
	}
	if _, err := ksuid.Parse(first.DatabaseID); err != nil {
		t.Fatal("deployment database ID", err)
	}
	gatewayWatch.expect(t, pb.EventType_EVENT_TYPE_CREATED, first.ID, name)
	firstDatabaseEvent, err := databaseWatch.Recv()
	if err != nil || firstDatabaseEvent.GetType() != pb.EventType_EVENT_TYPE_CREATED || firstDatabaseEvent.GetResourceId() != first.DatabaseID {
		t.Fatal("default database watch", firstDatabaseEvent, err)
	}
	firstDatabase := firstDatabaseEvent.ManagedDatabase
	namespace, err := gateways.DatabaseNamespace(first.DatabaseID)
	if err != nil || firstDatabase.Provider != gateways.ProviderDeployment || firstDatabase.Namespace != namespace || firstDatabase.Name != "gw-"+name+"-db" || len(firstDatabase.Name) != 261 {
		t.Fatal("deployment database shape", firstDatabase, err)
	}
	fetched, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: first.ID})
	if err != nil || fetched.Gateway.DatabaseId != first.DatabaseID {
		t.Fatal("gRPC default placement read", fetched, err)
	}
	code, body = requestJSON(t, "GET", base+"/managed_databases/"+first.DatabaseID, alice, nil)
	var restDatabase httpapi.ManagedDatabase
	if code != 200 || json.Unmarshal(body, &restDatabase) != nil || restDatabase.Namespace != firstDatabase.Namespace || restDatabase.Name != firstDatabase.Name {
		t.Fatal("REST deployment database read", code, string(body))
	}
	if strings.Contains(string(body), `"cluster_id"`) {
		t.Fatal("public database response exposed private placement")
	}
	readPlacement := func(id string, deleted bool) {
		t.Helper()
		read, err := rpc.WithRetainedResourceRead(call(controller))
		if err != nil {
			t.Fatal(err)
		}
		var header metadata.MD
		response, err := databases.GetManagedDatabase(read, &pb.GetManagedDatabaseRequest{Id: id}, grpc.Header(&header))
		if err != nil || response.GetManagedDatabase().GetMetadata().GetId() != id {
			t.Fatal("retained placement read", err)
		}
		got, err := databaseplacement.Read(header)
		if err != nil || got != cluster.ID {
			t.Fatal("recorded cluster differs", err)
		}
		_, gotDeleted, err := rpc.ObservedResourceState(header)
		if err != nil || gotDeleted != deleted {
			t.Fatal("placement deletion state differs", err)
		}
	}
	readPlacement(first.DatabaseID, false)
	for _, bearer := range []string{admin, alice, controller} {
		var header metadata.MD
		if _, err := databases.GetManagedDatabase(call(bearer), &pb.GetManagedDatabaseRequest{Id: first.DatabaseID}, grpc.Header(&header)); err != nil {
			t.Fatal(err)
		}
		if len(header.Get("hypershell-database-placement")) != 0 || len(header.Get("hypershell-database-cluster-id")) != 0 {
			t.Fatal("public read exposed placement metadata")
		}
	}
	for _, bearer := range []string{admin, alice, bob} {
		read, err := rpc.WithRetainedResourceRead(call(bearer))
		if err != nil {
			t.Fatal(err)
		}
		var header metadata.MD
		_, err = databases.GetManagedDatabase(read, &pb.GetManagedDatabaseRequest{Id: first.DatabaseID}, grpc.Header(&header))
		if status.Code(err) != codes.PermissionDenied || len(header.Get("hypershell-database-placement")) != 0 || len(header.Get("hypershell-database-cluster-id")) != 0 {
			t.Fatal("denied read exposed placement", err)
		}
	}
	foreign := ksuid.New().String()
	if err := f.storage.Create(ctx, "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: foreign}, Name: "foreign", Provider: "kubernetes", KubeconfigSecret: "foreign-ref"}); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err := f.db.QueryRow("SELECT stego_revision FROM gateways WHERE id=$1", first.ID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if code, _ := requestJSON(t, "PATCH", base+"/gateways/"+first.ID, owner, []byte(fmt.Sprintf(`{"cluster_id":%q}`, foreign))); code != 409 {
		t.Fatal("REST accepted database migration", code)
	}
	if _, err := client.UpdateGateway(call(owner), &pb.UpdateGatewayRequest{Id: first.ID, ClusterId: &foreign}); status.Code(err) != codes.AlreadyExists {
		t.Fatal("gRPC accepted database migration", err)
	}
	var unchanged int64
	if err := f.db.QueryRow("SELECT stego_revision FROM gateways WHERE id=$1", first.ID).Scan(&unchanged); err != nil || unchanged != revision {
		t.Fatal("rejected move changed the Gateway", err)
	}
	for _, invalid := range []string{"", "bad", ksuid.New().String()} {
		if code, _ := requestJSON(t, "PATCH", base+"/gateways/"+first.ID, owner, []byte(fmt.Sprintf(`{"cluster_id":%q}`, invalid))); code != 400 {
			t.Fatal("REST invalid cluster result changed", code)
		}
		if _, err := client.UpdateGateway(call(owner), &pb.UpdateGatewayRequest{Id: first.ID, ClusterId: &invalid}); status.Code(err) != codes.InvalidArgument {
			t.Fatal("gRPC invalid cluster result changed", err)
		}
	}
	if code, _ := requestJSON(t, "PATCH", base+"/gateways/"+first.ID, owner, []byte(fmt.Sprintf(`{"cluster_id":%q}`, cluster.ID))); code != 200 {
		t.Fatal("same-cluster patch failed", code)
	}
	// Another creator cannot select the first Gateway's private database.
	second, err := client.CreateGateway(call(bob), &pb.CreateGatewayRequest{Name: "second", ClusterId: cluster.ID, ReleaseId: release.ID, DatabaseId: first.DatabaseID})
	if err != nil || second.Gateway.DatabaseId == first.DatabaseID || second.Gateway.DatabaseId == "" {
		t.Fatal("private database isolation", second, err)
	}
	secondDatabaseEvent, err := databaseWatch.Recv()
	if err != nil || secondDatabaseEvent.GetType() != pb.EventType_EVENT_TYPE_CREATED || secondDatabaseEvent.ResourceId != second.Gateway.DatabaseId || secondDatabaseEvent.ManagedDatabase.Namespace == firstDatabase.Namespace {
		t.Fatal("second database watch", secondDatabaseEvent, err)
	}
	if _, err := client.GetGateway(call(alice), &pb.GetGatewayRequest{Id: second.Gateway.Metadata.Id}); status.Code(err) != codes.NotFound {
		t.Fatal("foreign Gateway read", err)
	}
	list, err := client.ListGateways(call(owner), &pb.ListGatewaysRequest{})
	if err != nil || list.GetMetadata().GetTotal() != 1 || len(list.Items) != 1 || list.Items[0].Metadata.Id != first.ID {
		t.Fatal("deployment filtered list", list, err)
	}
	patched, err := client.UpdateGateway(call(owner), &pb.UpdateGatewayRequest{Id: first.ID, DatabaseId: proto.String(second.Gateway.DatabaseId)})
	if err != nil || patched.Gateway.DatabaseId != first.DatabaseID {
		t.Fatal("gRPC placement reassignment", patched, err)
	}
	code, body = requestJSON(t, "PATCH", base+"/gateways/"+first.ID, owner, []byte(fmt.Sprintf(`{"database_id":%q}`, second.Gateway.DatabaseId)))
	if code != 200 {
		t.Fatal("REST placement placeholder patch", code, string(body))
	}
	var patchedREST httpapi.Gateway
	if json.Unmarshal(body, &patchedREST) != nil || patchedREST.DatabaseID != first.DatabaseID {
		t.Fatal("REST changed placement", string(body))
	}
	gatewayWatch.cancel()
	for _, bad := range []string{
		fmt.Sprintf(`{"name":"missing","cluster_id":%q,"release_id":%q}`, cluster.ID, release.ID),
		fmt.Sprintf(`{"name":"null","cluster_id":%q,"release_id":%q,"database_id":null}`, cluster.ID, release.ID),
	} {
		if code, _ := requestJSON(t, "POST", base+"/gateways", alice, []byte(bad)); code != 400 {
			t.Fatal("required placement placeholder", code)
		}
	}
	if code, _ := requestJSON(t, "POST", base+"/gateways", owner, encoded); code != 403 {
		t.Fatal("creator removal retained creation", code)
	}
	if code, _ := requestJSON(t, "POST", base+"/managed_databases", alice, []byte(`{"name":"bypass","provider":"deployment"}`)); code != 403 {
		t.Fatal("Gateway creation gave catalog write access", code)
	}
	if count(t, f.db, "managed_databases") != 2 {
		t.Fatal("denied request created a database")
	}
	grants := pb.NewRoleBindingServiceClient(connection)
	user := currentUser(t, base, owner)
	bindings, err := grants.ListRoleBindings(call(owner), &pb.ListRoleBindingsRequest{UserId: &user.ID, GatewayId: &first.ID})
	if err != nil || len(bindings.GetItems()) != 1 || bindings.Items[0].GetRoleName() != "gateway:owner" {
		t.Fatal("deployment owner grant", bindings, err)
	}
	ownerID := bindings.Items[0].Metadata.Id
	readCatalogEvent(t, kafkaConsumer(t, config), first.DatabaseID, "ManagedDatabases", "Create", "manageddatabase.created")
	readEvent(t, kafkaConsumer(t, config), first.ID)
	readGrantEvent(t, kafkaConsumer(t, config), ownerID, first.ID, "Create", "rolebinding.created")
	if code, _ := requestJSON(t, "DELETE", base+"/managed_databases/"+first.DatabaseID, admin, nil); code != 409 {
		t.Fatal("private database deleted while in use", code)
	}
	awaitQueueEmpty(t, f)
	stop()
	service, err := gateways.New(f.storage)
	if err != nil {
		t.Fatal(err)
	}
	offline, err := service.Create(ctx, principal("alice", "gateway:creator"), gateways.CreateRequest{Name: "offline", ClusterID: cluster.ID, ReleaseID: release.ID, DatabaseID: first.DatabaseID})
	if err != nil {
		t.Fatal(err)
	}
	if offline.DatabaseID == first.DatabaseID || offline.DatabaseID == second.Gateway.DatabaseId || count(t, f.db, "stego_outbox.messages") != 3 {
		t.Fatal("offline placement did not retain three events")
	}
	rows, err := f.db.Query(`SELECT kind,resource_key,id::text FROM stego_outbox.messages ORDER BY sequence`)
	if err != nil {
		t.Fatal(err)
	}
	type notice struct{ kind, key, id string }
	var notices []notice
	for rows.Next() {
		var n notice
		if err := rows.Scan(&n.kind, &n.key, &n.id); err != nil {
			t.Fatal(err)
		}
		notices = append(notices, n)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	settings = append(settings, "DATABASE_PROVIDER=deployment")
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	base = address + "/api/hypershell/v1"
	client, connection = grpcClient(t, grpcAddress, tlsIdentity)
	databases = pb.NewManagedDatabaseServiceClient(connection)
	fetched, err = client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: offline.ID})
	if err != nil || fetched.Gateway.DatabaseId != offline.DatabaseID {
		t.Fatal("restart placement", fetched, err)
	}
	dbRead, err := databases.GetManagedDatabase(call(alice), &pb.GetManagedDatabaseRequest{Id: offline.DatabaseID})
	if err != nil || dbRead.ManagedDatabase.Provider != gateways.ProviderDeployment || dbRead.ManagedDatabase.Name != "gw-offline-db" {
		t.Fatal("restart database", dbRead, err)
	}
	readPlacement(first.DatabaseID, false)
	readPlacement(offline.DatabaseID, false)
	for _, n := range notices {
		switch n.kind {
		case "manageddatabase.created":
			readCatalogEvent(t, kafkaConsumer(t, config), n.key, "ManagedDatabases", "Create", n.kind, n.id)
		case "gateway.created":
			readCatalogEvent(t, kafkaConsumer(t, config), n.key, "Gateways", "Create", n.kind, n.id)
		case "rolebinding.created":
			if actual := readGrantEvent(t, kafkaConsumer(t, config), n.key, offline.ID, "Create", n.kind); actual != n.id {
				t.Fatal("owner event ID changed on restart")
			}
		default:
			t.Fatal("unexpected offline event", n.kind)
		}
	}
	if code, _ := requestJSON(t, "DELETE", base+"/gateways/"+first.ID, owner, nil); code != 204 {
		t.Fatal("Gateway removal", code)
	}
	if code, _ := requestJSON(t, "DELETE", base+"/managed_databases/"+first.DatabaseID, admin, nil); code != 204 {
		t.Fatal("unused private database removal", code)
	}
	readPlacement(first.DatabaseID, true)
	if _, err := databases.GetManagedDatabase(call(admin), &pb.GetManagedDatabaseRequest{Id: second.Gateway.DatabaseId}); err != nil {
		t.Fatal("cleanup changed another database", err)
	}
}

func TestDeploymentPlacementFailureIsAtomic(t *testing.T) {
	failures := map[string]string{
		"placement":      `ALTER TABLE managed_databases ADD CONSTRAINT reject_placement CHECK (cluster_id IS NULL)`,
		"database":       `ALTER TABLE managed_databases ADD CONSTRAINT reject_deployment CHECK (provider <> 'deployment')`,
		"Gateway":        `ALTER TABLE gateways ADD CONSTRAINT reject_gateway CHECK (name <> 'blocked')`,
		"owner":          `ALTER TABLE role_bindings ADD CONSTRAINT reject_owner CHECK (scope <> 'gateway')`,
		"database event": `ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_event CHECK (kind <> 'manageddatabase.created')`,
		"owner event":    `ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_event CHECK (kind <> 'rolebinding.created')`,
		"Gateway event":  `ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_event CHECK (kind <> 'gateway.created')`,
	}
	for name, query := range failures {
		t.Run(name, func(t *testing.T) {
			f := database(t)
			service, err := gateways.New(f.storage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			row, err := service.Create(context.Background(), principal("alice", "gateway:creator"), f.request("blocked"))
			if err == nil || row.ID != "" {
				t.Fatal("failed placement reported success", row, err)
			}
			if count(t, f.db, "managed_databases") != 1 {
				t.Fatal("failed placement changed the database catalog")
			}
			for _, table := range []string{"gateways", "role_bindings", "users", "stego_outbox.messages"} {
				if count(t, f.db, table) != 0 {
					t.Fatal("failed placement left data", table)
				}
			}
		})
	}
}

func TestDeploymentPlacementConcurrencyAndMigration(t *testing.T) {
	f := database(t)
	service, err := gateways.New(f.storage)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	p := principal("alice", "gateway:creator")
	if err := service.PrepareRequest(ctx, p); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	rows := make(chan model.Gateway, 8)
	failures := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for attempt := 0; attempt < 16; attempt++ {
				row, err := service.Create(ctx, p, f.request("parallel"))
				if errors.Is(err, storage.ErrSerialization) || errors.Is(err, storage.ErrConflict) {
					continue
				}
				if err != nil {
					failures <- err
				} else {
					rows <- row
				}
				return
			}
			failures <- errors.New("placement retry limit reached")
		}()
	}
	wg.Wait()
	close(rows)
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	namespaces := map[string]bool{}
	for row := range rows {
		db, err := f.storage.Get(ctx, "ManagedDatabase", row.DatabaseID)
		if err != nil {
			t.Fatal(err)
		}
		database := db.(model.ManagedDatabase)
		if ids[row.DatabaseID] || namespaces[database.Namespace] || database.Provider != gateways.ProviderDeployment || database.ClusterID == nil || *database.ClusterID != row.ClusterID {
			t.Fatal("concurrent placement shared a database")
		}
		ids[row.DatabaseID] = true
		namespaces[database.Namespace] = true
	}
	if len(ids) != 8 || count(t, f.db, "gateways") != 8 || count(t, f.db, "managed_databases") != 9 || count(t, f.db, "role_bindings") != 9 || count(t, f.db, "stego_outbox.messages") != 25 {
		t.Fatal("concurrent placement counts", len(ids))
	}
	// Widen the old name column without changing existing data or times.
	before, err := f.storage.Get(ctx, "ManagedDatabase", f.database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`ALTER TABLE managed_databases ALTER COLUMN name TYPE varchar(255)`); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../migrations/000007_deployment_database_names.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.db.Exec(string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	after, err := f.storage.Get(ctx, "ManagedDatabase", f.database)
	if err != nil {
		t.Fatal(err)
	}
	a, b := before.(model.ManagedDatabase), after.(model.ManagedDatabase)
	if a.ID != b.ID || a.Name != b.Name || a.Namespace != b.Namespace || !a.CreatedTime.Equal(b.CreatedTime) || !a.UpdatedTime.Equal(b.UpdatedTime) {
		t.Fatal("name migration changed existing placement")
	}
	// Explicit CNPG mode does not silently choose one of the deployment records.
	if _, err := f.service.Create(ctx, p, f.request("ambiguous-cnpg")); !errors.Is(err, gateways.ErrInvalid) {
		t.Fatal("CNPG selected an ambiguous database", err)
	}
}

func TestGeneratedRuntimeRejectsUnknownDatabaseProvider(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Env = applicationEnvironment(t, f.dsn, config, "DATABASE_PROVIDER=private-unknown-provider")
	output, err := command.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || ctx.Err() != nil {
		t.Fatal("invalid provider did not stop startup with exit code 1", err)
	}
	for _, forbidden := range []string{"private-unknown-provider", "DATABASE_PROVIDER must be", "starting server", "gRPC server started"} {
		if strings.Contains(string(output), forbidden) {
			t.Fatal("invalid provider exposed error text or started a listener")
		}
	}
	failures := 0
	for _, line := range strings.Split(string(output), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil || record["event.name"] != "service.failed" {
			continue
		}
		failures++
		// The generated application.NewHandler call owns provider validation.
		if len(record) != 5 || record["stage"] != "component[4].constructor[0]" || record["severity"] != "ERROR" || record["message"] != "Service failed" {
			t.Fatal("provider startup failure lost its safe stage", record)
		}
	}
	if failures != 1 {
		t.Fatal("provider startup did not report one failure", failures)
	}
}

func BenchmarkDeploymentPlacement(b *testing.B) {
	f := database(b)
	service, err := gateways.New(f.storage)
	if err != nil {
		b.Fatal(err)
	}
	ctx := context.Background()
	p := principal("creator", "gateway:creator")
	if err := service.PrepareRequest(ctx, p); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		if _, err := service.Create(ctx, p, f.request("benchmark")); err != nil {
			b.Fatal(err)
		}
	}
}

func TestDatabaseClusterPlacementMigrationAndRetention(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	service, err := gateways.New(f.storage)
	if err != nil {
		t.Fatal(err)
	}
	owner := principal("owner", "gateway:creator")
	gateway, err := service.Create(ctx, owner, f.request("placement"))
	if err != nil {
		t.Fatal(err)
	}
	value, err := f.storage.Get(ctx, "ManagedDatabase", gateway.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	placed := value.(model.ManagedDatabase)
	if placed.ClusterID == nil || *placed.ClusterID != f.cluster {
		t.Fatal("new database has no recorded cluster")
	}
	catalogService, err := catalog.New(f.storage, service)
	if err != nil {
		t.Fatal(err)
	}
	admin := principal("admin", "platform:admin")
	if err := service.Delete(ctx, owner, gateway.ID); err != nil {
		t.Fatal(err)
	}
	if err := catalogService.Clusters.Delete(ctx, admin, f.cluster); !errors.Is(err, storage.ErrConflict) {
		t.Fatal("cluster deletion ignored its live database", err)
	}
	if err := catalogService.Databases.Delete(ctx, admin, gateway.DatabaseID); err != nil {
		t.Fatal(err)
	}
	if err := catalogService.Clusters.Delete(ctx, admin, f.cluster); err != nil {
		t.Fatal("unused cluster deletion", err)
	}
	var retained string
	if err := f.db.QueryRowContext(ctx, "SELECT cluster_id FROM managed_databases WHERE id=$1 AND deleted_at IS NOT NULL", gateway.DatabaseID).Scan(&retained); err != nil || retained != f.cluster {
		t.Fatal("deleted database lost placement", err)
	}
	// Model an old schema. A current or deleted Gateway does not prove where its
	// database was created. The migration must leave old records unassigned.
	before, err := f.storage.Get(ctx, "ManagedDatabase", f.database)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, "ALTER TABLE managed_databases DROP COLUMN cluster_id CASCADE"); err != nil {
		t.Fatal(err)
	}
	migration, err := os.ReadFile("../migrations/000009_database_cluster_placement.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.db.ExecContext(ctx, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	var assigned int
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM managed_databases WHERE cluster_id IS NOT NULL").Scan(&assigned); err != nil || assigned != 0 {
		t.Fatal("migration inferred an old placement", err)
	}
	after, err := f.storage.Get(ctx, "ManagedDatabase", f.database)
	if err != nil {
		t.Fatal(err)
	}
	a, b := before.(model.ManagedDatabase), after.(model.ManagedDatabase)
	if a.ID != b.ID || a.Namespace != b.Namespace || a.ResourceVersion != b.ResourceVersion || !a.UpdatedTime.Equal(b.UpdatedTime) || !a.CreatedTime.Equal(b.CreatedTime) || b.ClusterID != nil {
		t.Fatal("migration changed existing record state")
	}
	if _, err := f.db.ExecContext(ctx, "UPDATE managed_databases SET cluster_id=$1 WHERE id=$2", ksuid.New().String(), f.database); err == nil {
		t.Fatal("placement accepted a missing cluster")
	}
}
