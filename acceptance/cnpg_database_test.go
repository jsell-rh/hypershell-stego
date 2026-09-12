package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/metadata"
)

func TestCNPGDatabaseWorkloadAndOfflineDeletion(t *testing.T) {
	if os.Getenv("STEGO_REQUIRE_CNPG") != "1" {
		t.Skip("run scripts/check-workload.sh cnpg")
	}
	k := kubernetesFixture(t)
	f := databaseSetup(t, false)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "DATABASE_PROVIDER=cnpg", `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	settings = withCleanupGrants(t, settings, cleanupGrant("controller", "ManagedDatabase", "provider", "cnpg"))
	settings = withControllerWriteGrants(t, settings, databaseWriteGrant("controller", "cnpg"))
	binary := buildApplication(t)
	controllerBinary := buildProgram(t, "./out/deploy/workers/database")
	stopAPI, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stopAPI() }()
	admin := token(t, key, "admin", "platform:admin")
	creator := token(t, key, "creator", "gateway:creator")
	controllerToken := token(t, key, "controller")
	code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/managed_databases", admin, []byte(`{"name":"shared-cnpg","provider":"cnpg"}`))
	var db httpapi.ManagedDatabase
	if code != 201 || json.Unmarshal(body, &db) != nil {
		t.Fatal("CNPG database creation", code, string(body))
	}
	namespace, err := gateways.DatabaseNamespace(db.ID)
	if err != nil || namespace != db.Namespace {
		t.Fatal("database namespace", err)
	}
	t.Cleanup(func() { k.must(t, "", "delete", "namespace", namespace, "--ignore-not-found=true", "--wait=false") })
	readCatalogEvent(t, consumer, db.ID, "ManagedDatabases", "Create", "manageddatabase.created")
	awaitQueueEmpty(t, f)
	if code, _ = requestJSON(t, "DELETE", address+"/api/hypershell/v1/managed_databases/"+db.ID, creator, nil); code != 403 {
		t.Fatal("ordinary subject deleted database catalog", code)
	}
	stopController, logs := startDatabaseController(t, controllerBinary, k, rpcAddress, tlsIdentity.config.CAFile, controllerToken, "DATABASE_PROVIDER=cnpg")
	defer func() { stopController() }()
	_, connection := grpcClient(t, rpcAddress, tlsIdentity)
	databases := pb.NewManagedDatabaseServiceClient(connection)
	call := func() context.Context {
		return metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+controllerToken))
	}
	awaitReady := func() {
		t.Helper()
		until := time.Now().Add(180 * time.Second)
		for {
			ctx, cancel := context.WithTimeout(call(), 3*time.Second)
			response, err := databases.GetManagedDatabase(ctx, &pb.GetManagedDatabaseRequest{Id: db.ID})
			cancel()
			if err == nil && response.GetManagedDatabase().GetStatus() == "ready" {
				if response.ManagedDatabase.GetConnectionSecret() != "" {
					t.Fatal("CNPG invented shared Gateway credentials")
				}
				return
			}
			if time.Now().After(until) {
				t.Fatalf("CNPG did not become ready: %v\n%s\n%s", err, logs(), k.must(t, "", "-n", namespace, "get", "clusters,pods", "-o", "wide"))
			}
			time.Sleep(time.Second)
		}
	}
	awaitReady()
	code, body = requestJSON(t, "POST", address+"/api/hypershell/v1/managed_clusters", admin, []byte(`{"name":"cluster","provider":"kubernetes","kubeconfig_secret":"cluster-ref"}`))
	var cluster httpapi.ManagedCluster
	if code != 201 || json.Unmarshal(body, &cluster) != nil {
		t.Fatal("cluster setup", code, string(body))
	}
	code, body = requestJSON(t, "POST", address+"/api/hypershell/v1/gateway_releases", admin, []byte(`{"name":"release","image":"registry.example/gateway:v1"}`))
	var release httpapi.GatewayRelease
	if code != 201 || json.Unmarshal(body, &release) != nil {
		t.Fatal("release setup", code, string(body))
	}
	// The same shared catalog record must be selected for a real Gateway.
	input, _ := json.Marshal(gateways.CreateRequest{Name: "cnpg-gateway", ClusterID: cluster.ID, ReleaseID: release.ID})
	code, body = requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", creator, input)
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &gateway) != nil || gateway.DatabaseID != db.ID {
		t.Fatal("CNPG Gateway placement", code, string(body))
	}
	if code, _ = requestJSON(t, "DELETE", address+"/api/hypershell/v1/managed_databases/"+db.ID, admin, nil); code != 409 {
		t.Fatal("deleted database with a live Gateway", code)
	}

	runSQL := func(name, sql string) string {
		t.Helper()
		script := `for i in $(seq 1 30); do if psql -Atc 'SELECT 1' >/dev/null 2>&1; then break; fi; sleep 1; done
psql -v ON_ERROR_STOP=1 -Atc '` + sql + `'
if PGSSLMODE=disable psql -Atc 'SELECT 1' >/dev/null 2>&1; then echo 'unencrypted SQL succeeded'; exit 1; fi
if PGDATABASE=postgres psql -Atc 'SELECT 1' >/dev/null 2>&1; then echo 'cross-database SQL succeeded'; exit 1; fi`
		k.apply(t, map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"restartPolicy": "Never", "automountServiceAccountToken": false, "securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 26, "runAsGroup": 26, "seccompProfile": map[string]any{"type": "RuntimeDefault"}}, "containers": []any{map[string]any{"name": "sql", "image": databasecontroller.CNPGPostgresImage, "command": []string{"sh", "-ec", script}, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "capabilities": map[string]any{"drop": []string{"ALL"}}}, "resources": map[string]any{"requests": map[string]string{"cpu": "10m", "memory": "32Mi"}, "limits": map[string]string{"cpu": "100m", "memory": "128Mi"}}, "env": []any{
			map[string]any{"name": "PGPASSWORD", "valueFrom": map[string]any{"secretKeyRef": map[string]string{"name": "openshell-db-app", "key": "password"}}},
			map[string]string{"name": "PGHOST", "value": "openshell-db-rw." + namespace + ".svc.cluster.local"}, map[string]string{"name": "PGUSER", "value": "openshell"}, map[string]string{"name": "PGDATABASE", "value": "openshell"}, map[string]string{"name": "PGSSLMODE", "value": "verify-full"}, map[string]string{"name": "PGSSLROOTCERT", "value": "/ca/ca.crt"}, map[string]string{"name": "PGCONNECT_TIMEOUT", "value": "3"}}, "volumeMounts": []any{map[string]any{"name": "ca", "mountPath": "/ca", "readOnly": true}}}}, "volumes": []any{map[string]any{"name": "ca", "secret": map[string]string{"secretName": "openshell-db-ca"}}}}})
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		output, err := k.command(ctx, "", "-n", namespace, "wait", "pod/"+name, "--for=jsonpath={.status.phase}=Succeeded", "--timeout=80s")
		cancel()
		logs := k.must(t, "", "-n", namespace, "logs", name)
		if err != nil {
			t.Fatalf("CNPG SQL failed: %v\n%s\n%s", err, output, logs)
		}
		return strings.TrimSpace(string(logs))
	}
	output := runSQL("cnpg-write", `SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid(); SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls FROM pg_roles WHERE rolname=current_user; SHOW data_checksums; CREATE TABLE acceptance_marker(value integer); INSERT INTO acceptance_marker VALUES (42);`)
	if !strings.HasPrefix(output, "t\nf\non\n") {
		t.Fatal("CNPG SQL did not use TLS and a restricted role", output)
	}
	// Record state that must survive controller and database Pod restart.
	before := strings.TrimSpace(string(k.must(t, "", "-n", namespace, "get", "secret", "openshell-db-app", "-o", "jsonpath={.metadata.uid}")))
	stopController()
	k.must(t, "", "-n", namespace, "patch", "cluster", databasecontroller.CNPGClusterName, "--type=merge", "-p", `{"spec":{"resources":{"limits":{"memory":"640Mi"}}}}`)
	stopController, logs = startDatabaseController(t, controllerBinary, k, rpcAddress, tlsIdentity.config.CAFile, controllerToken, "DATABASE_PROVIDER=cnpg")
	until := time.Now().Add(60 * time.Second)
	for strings.TrimSpace(string(k.must(t, "", "-n", namespace, "get", "cluster", databasecontroller.CNPGClusterName, "-o", "jsonpath={.spec.resources.limits.memory}"))) != "512Mi" {
		if time.Now().After(until) {
			t.Fatalf("ready CNPG Cluster drift was not repaired\n%s", logs())
		}
		time.Sleep(time.Second)
	}
	primary := strings.TrimSpace(string(k.must(t, "", "-n", namespace, "get", "cluster", databasecontroller.CNPGClusterName, "-o", "jsonpath={.status.currentPrimary}")))
	k.must(t, "", "-n", namespace, "delete", "pod", "-l", "cnpg.io/cluster="+databasecontroller.CNPGClusterName, "--wait=true")
	k.must(t, "", "-n", namespace, "wait", "pod/"+primary, "--for=create", "--timeout=90s")
	k.must(t, "", "-n", namespace, "wait", "pods", "-l", "cnpg.io/cluster="+databasecontroller.CNPGClusterName, "--for=condition=Ready", "--timeout=90s")
	awaitReady()
	if output = runSQL("cnpg-read", `SELECT value FROM acceptance_marker`); output != "42" {
		t.Fatal("CNPG lost persisted data", output)
	}
	after := strings.TrimSpace(string(k.must(t, "", "-n", namespace, "get", "secret", "openshell-db-app", "-o", "jsonpath={.metadata.uid}")))
	if before == "" || before != after {
		t.Fatal("CNPG replaced database credentials")
	}
	provider, err := databasecontroller.NewCNPG(k.options)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	row := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: db.ID}, Namespace: namespace, Provider: "cnpg"}
	generation := strings.TrimSpace(string(k.must(t, "", "-n", namespace, "get", "cluster", databasecontroller.CNPGClusterName, "-o", "jsonpath={.metadata.generation}")))
	started := time.Now()
	for i := 0; i < 5; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err = provider.Ensure(ctx, row)
		cancel()
		if err != nil {
			t.Fatal("stable CNPG reconciliation", err)
		}
	}
	t.Logf("Five stable CNPG reconciliations: %s", time.Since(started))
	if actual := strings.TrimSpace(string(k.must(t, "", "-n", namespace, "get", "cluster", databasecontroller.CNPGClusterName, "-o", "jsonpath={.metadata.generation}"))); actual != generation {
		t.Fatal("stable reconciliation changed the Cluster specification")
	}
	// Delete while the controller is stopped, then restart the API as well.
	stopController()
	if code, body = requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+gateway.ID, creator, nil); code != 204 {
		t.Fatal("Gateway deletion", code, string(body))
	}
	if code, body = requestJSON(t, "DELETE", address+"/api/hypershell/v1/managed_databases/"+db.ID, admin, nil); code != 204 {
		t.Fatal("CNPG deletion", code, string(body))
	}
	if code, _ = requestJSON(t, "GET", address+"/api/hypershell/v1/managed_databases/"+db.ID, admin, nil); code != 404 {
		t.Fatal("deleted database remained public", code)
	}
	connection.Close()
	stopAPI()
	stopAPI, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, rpcAddress, tlsIdentity)
	cleanup := control.NewDatabaseCleanupServiceClient(connection)
	summary := func(want int64) {
		t.Helper()
		ctx, cancel := context.WithTimeout(call(), 5*time.Second)
		defer cancel()
		got, err := cleanup.GetDatabaseCleanupSummary(ctx, &control.GetDatabaseCleanupSummaryRequest{Owner: "provider", Provider: "cnpg"})
		if err != nil || got.GetPending() != want {
			t.Fatal("CNPG cleanup summary", got, err)
		}
	}
	summary(1)
	// A forbidden Cluster deletion must retain both the namespace and obligation.
	role := k.options.ClusterIssuer
	k.must(t, "", "patch", "clusterrole", role, "--type=json", "-p", `[{"op":"replace","path":"/rules/2/verbs","value":["get","create","patch"]}]`)
	stopController, logs = startDatabaseController(t, controllerBinary, k, rpcAddress, tlsIdentity.config.CAFile, controllerToken, "DATABASE_PROVIDER=cnpg")
	until = time.Now().Add(30 * time.Second)
	for !controllerRetryLogged(logs()) {
		if time.Now().After(until) {
			t.Fatalf("CNPG cleanup denial was lost\n%s", logs())
		}
		time.Sleep(100 * time.Millisecond)
	}
	requireDatabaseDeleteDenied(t, func(ctx context.Context) error { return provider.Delete(ctx, row) })
	summary(1)
	k.must(t, "", "get", "namespace", namespace)
	k.must(t, "", "patch", "clusterrole", role, "--type=json", "-p", `[{"op":"replace","path":"/rules/2/verbs","value":["get","create","patch","delete"]}]`)
	k.must(t, "", "wait", "--for=delete", "namespace/"+namespace, "--timeout=90s")
	until = time.Now().Add(30 * time.Second)
	for {
		var complete bool
		if err = f.db.QueryRow("SELECT (stego_cleanup->>'provider')::boolean FROM managed_databases WHERE id=$1", db.ID).Scan(&complete); err != nil {
			t.Fatal(err)
		}
		if complete {
			break
		}
		if time.Now().After(until) {
			t.Fatalf("CNPG cleanup was not committed\n%s", logs())
		}
		time.Sleep(100 * time.Millisecond)
	}
	summary(0)
}
