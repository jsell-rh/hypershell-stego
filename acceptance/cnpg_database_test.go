package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"google.golang.org/grpc/metadata"
)

func TestCNPGDatabaseWorkloadAndOfflineDeletion(t *testing.T) {
	if os.Getenv("STEGO_REQUIRE_CNPG") != "1" {
		t.Skip("run scripts/check-workload.sh cnpg")
	}
	f := databaseCatalogFixture(t)
	workers := allocatedKubernetesFixture(t, f.cluster, "namespace-allocation", "database")
	k, allocatorKube := workers["database"], workers["namespace-allocation"]
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "DATABASE_PROVIDER=cnpg", `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller","allocator","cleanup-probe"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	settings = withCleanupGrants(t, settings, cleanupGrant("controller", "ManagedDatabase", "provider", f.cluster), cleanupGrant("cleanup-probe", "Gateway", "workload", f.cluster))
	settings = withControllerWriteGrants(t, settings, databaseWriteGrant("controller", f.cluster))
	binary := buildApplication(t)
	controllerBinary := buildProgram(t, "./out/deploy/workers/database")
	allocatorBinary := buildProgram(t, "./out/deploy/workers/namespace-allocation")
	stopAPI, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stopAPI() }()
	admin := token(t, key, "admin", "platform:admin")
	creator := token(t, key, "creator", "gateway:creator")
	controllerToken := token(t, key, "controller")
	code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/managed_databases", admin, databaseCreateBody(t, "shared-cnpg", "cnpg", f.cluster))
	var db httpapi.ManagedDatabase
	if code != 201 || json.Unmarshal(body, &db) != nil {
		t.Fatal("CNPG database creation", code, string(body))
	}
	namespace, err := gateways.DatabaseNamespace(db.ID)
	if err != nil || namespace != db.Namespace {
		t.Fatal("database namespace", err)
	}
	allocatorKube.cleanupAllocatedNamespace(t, "database", namespace, db.ID)
	readCatalogEvent(t, consumer, db.ID, "ManagedDatabases", "Create", "manageddatabase.created")
	awaitQueueEmpty(t, f)
	if code, _ = requestJSON(t, "DELETE", address+"/api/hypershell/v1/managed_databases/"+db.ID, creator, nil); code != 403 {
		t.Fatal("ordinary subject deleted database catalog", code)
	}
	prepareCNPGTestOperator(t, namespace, db.ID)
	workerSettings := []string{"DATABASE_PROVIDER=cnpg", "HYPERSHELL_MANAGED_CLUSTER_ID=" + f.cluster, "HYPERSHELL_CONTROL_NAMESPACE=" + k.options.ControlNamespace}
	allocatorToken := token(t, key, "allocator")
	stopAllocator, allocatorLogs := startDatabaseController(t, allocatorBinary, allocatorKube, rpcAddress, tlsIdentity.config.CAFile, allocatorToken, workerSettings...)
	defer func() { stopAllocator() }()
	startCNPGTestOperator(t, allocatorKube, namespace, db.ID)
	stopController, logs := startDatabaseController(t, controllerBinary, k, rpcAddress, tlsIdentity.config.CAFile, controllerToken, workerSettings...)
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
				t.Fatalf("CNPG did not become ready: %v\n%s\n%s", err, logs()+allocatorLogs(), k.must(t, "", "-n", namespace, "get", "clusters,pods", "-o", "wide"))
			}
			time.Sleep(time.Second)
		}
	}
	awaitReady()
	// The database worker can manage only an allocated database namespace.
	databaseClient := k.workerClient(t)
	defer databaseClient.Close()
	requireWorkerPermission(t, databaseClient, "create", "postgresql.cnpg.io", "clusters", namespace, true)
	for _, permission := range []struct{ verb, group, resource, namespace string }{
		{"create", "postgresql.cnpg.io", "clusters", k.options.ControlNamespace},
		{"delete", "", "namespaces", ""},
		{"get", "", "secrets", namespace},
		{"create", "rbac.authorization.k8s.io", "rolebindings", namespace},
	} {
		requireWorkerPermission(t, databaseClient, permission.verb, permission.group, permission.resource, permission.namespace, false)
	}
	for _, path := range []string{"/api/v1/namespaces/" + namespace + "/secrets/openshell-db-app", "/apis/postgresql.cnpg.io/v1/namespaces/" + k.options.ControlNamespace + "/clusters/unrelated"} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, status, err := databaseClient.Request(ctx, "GET", path, nil)
		cancel()
		var denied *kube.APIError
		if status != 403 || !errors.As(err, &denied) || denied.StatusCode != 403 {
			t.Fatal("database worker access was not denied", status, err)
		}
	}
	code, body = requestJSON(t, "POST", address+"/api/hypershell/v1/gateway_releases", admin, []byte(`{"name":"release","image":"registry.example/gateway:v1"}`))
	var release httpapi.GatewayRelease
	if code != 201 || json.Unmarshal(body, &release) != nil {
		t.Fatal("release setup", code, string(body))
	}
	// The same shared catalog record must be selected for a real Gateway.
	input, _ := json.Marshal(gateways.CreateRequest{Name: "cnpg-gateway", ClusterID: f.cluster, ReleaseID: release.ID})
	code, body = requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", creator, input)
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &gateway) != nil || gateway.DatabaseID != db.ID {
		t.Fatal("CNPG Gateway placement", code, string(body))
	}
	allocatorKube.cleanupAllocatedNamespace(t, "gateway", gateway.Namespace, gateway.ID)
	if code, _ = requestJSON(t, "DELETE", address+"/api/hypershell/v1/managed_databases/"+db.ID, admin, nil); code != 409 {
		t.Fatal("deleted database with a live Gateway", code)
	}

	runSQL := func(name, sql string) string {
		t.Helper()
		script := `for i in $(seq 1 30); do if psql -Atc 'SELECT 1' >/dev/null 2>&1; then break; fi; sleep 1; done
psql -v ON_ERROR_STOP=1 -Atc '` + sql + `'
if PGSSLMODE=disable psql -Atc 'SELECT 1' >/dev/null 2>&1; then echo 'unencrypted SQL succeeded'; exit 1; fi
if PGDATABASE=postgres psql -Atc 'SELECT 1' >/dev/null 2>&1; then echo 'cross-database SQL succeeded'; exit 1; fi`
		k.apply(t, map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": name, "namespace": namespace}, "spec": map[string]any{"restartPolicy": "Never", "activeDeadlineSeconds": 90, "automountServiceAccountToken": false, "securityContext": map[string]any{"runAsNonRoot": true, "seccompProfile": map[string]any{"type": "RuntimeDefault"}}, "containers": []any{map[string]any{"name": "sql", "image": databasecontroller.CNPGPostgresImage, "command": []string{"sh", "-ec", script}, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "capabilities": map[string]any{"drop": []string{"ALL"}}}, "resources": map[string]any{"requests": map[string]string{"cpu": "10m", "memory": "32Mi", "ephemeral-storage": "16Mi"}, "limits": map[string]string{"cpu": "100m", "memory": "128Mi", "ephemeral-storage": "64Mi"}}, "env": []any{
			map[string]any{"name": "PGPASSWORD", "valueFrom": map[string]any{"secretKeyRef": map[string]string{"name": "openshell-db-app", "key": "password"}}},
			map[string]string{"name": "PGHOST", "value": "openshell-db-rw." + namespace + ".svc.cluster.local"}, map[string]string{"name": "PGUSER", "value": "openshell"}, map[string]string{"name": "PGDATABASE", "value": "openshell"}, map[string]string{"name": "PGSSLMODE", "value": "verify-full"}, map[string]string{"name": "PGSSLROOTCERT", "value": "/ca/ca.crt"}, map[string]string{"name": "PGCONNECT_TIMEOUT", "value": "3"}}, "volumeMounts": []any{map[string]any{"name": "ca", "mountPath": "/ca", "readOnly": true}}}}, "volumes": []any{map[string]any{"name": "ca", "secret": map[string]string{"secretName": "openshell-db-ca"}}}}})
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		output, err := k.command(ctx, "", "-n", namespace, "wait", "pod/"+name, "--for=jsonpath={.status.phase}=Succeeded", "--timeout=80s")
		cancel()
		logs := k.must(t, "", "-n", namespace, "logs", name)
		if err != nil {
			t.Fatalf("CNPG SQL failed: %v\n%s\n%s", err, output, logs)
		}
		k.must(t, "", "-n", namespace, "delete", "pod", name, "--wait=true", "--timeout=30s")
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
	stopController, logs = startDatabaseController(t, controllerBinary, k, rpcAddress, tlsIdentity.config.CAFile, controllerToken, workerSettings...)
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
	row := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: db.ID}, Namespace: namespace, Provider: "cnpg", ClusterId: &f.cluster}
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
	// Delete while both workers are stopped, then restart the API as well.
	stopController()
	stopAllocator()
	if code, body = requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+gateway.ID, creator, nil); code != 204 {
		t.Fatal("Gateway deletion", code, string(body))
	}
	if code, _ = requestJSON(t, "DELETE", address+"/api/hypershell/v1/managed_databases/"+db.ID, admin, nil); code != 409 {
		t.Fatal("database deletion bypassed pending Gateway cleanup", code)
	}
	// This catalog-link probe never starts a Gateway workload or creates its SQL
	// database. Confirm its cleanup only after the common allocator removes its
	// namespace. The browser workflow tests a complete Gateway and SQL cleanup.
	probeClient := allocatorKube.workerClient(t)
	defer probeClient.Close()
	probeAllocator, err := allocation.New(probeClient, allocatorKube.options.ControlNamespace)
	if err != nil {
		t.Fatal(err)
	}
	probeContext, probeCancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer probeCancel()
	for {
		gone, err := probeAllocator.Delete(probeContext, "gateway", gateway.Namespace, gateway.ID)
		if err != nil && !errors.Is(err, allocation.ErrPending) {
			t.Fatal("catalog Gateway namespace cleanup", err)
		}
		if gone {
			break
		}
		if probeContext.Err() != nil {
			t.Fatal("catalog Gateway namespace cleanup timed out")
		}
		time.Sleep(time.Second)
	}
	state := control.NewGatewayIdentityServiceClient(connection)
	probeCall := metadata.NewOutgoingContext(probeContext, metadata.Pairs("authorization", "Bearer "+token(t, key, "cleanup-probe")))
	current, err := state.GetGatewayIdentityState(probeCall, &control.GetGatewayIdentityStateRequest{Id: gateway.ID})
	if err != nil || !current.GetDeleted() || current.GetGateway().GetNamespace() != gateway.Namespace || current.GetGateway().GetClusterId() != f.cluster {
		t.Fatal("catalog Gateway cleanup state", err)
	}
	write, err := rpc.WithResourceVersion(probeCall, current.GetResourceVersion())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.ObserveGatewayCleanup(write, &control.ObserveGatewayCleanupRequest{Id: gateway.ID, Owner: "workload", Target: f.cluster, Complete: true}); err != nil {
		t.Fatal("catalog Gateway cleanup observation", err)
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
		got, err := cleanup.GetDatabaseCleanupSummary(ctx, &control.GetDatabaseCleanupSummaryRequest{Owner: "provider", Provider: "cnpg", ClusterId: f.cluster})
		if err != nil || got.GetPending() != want {
			t.Fatal("CNPG cleanup summary", got, err)
		}
	}
	summary(1)
	// Remove only namespace deletion from the allocator's generated role.
	// The database worker must keep its cleanup record until removal is observed.
	role := allocatorKube.options.ControlNamespace + ".hypershell-namespace-allocation"
	original := k.must(t, "", "get", "clusterrole", role, "-o", "json")
	var roleObject map[string]any
	if json.Unmarshal(original, &roleObject) != nil {
		t.Fatal("invalid allocator role")
	}
	originalRules := roleObject["rules"]
	rules, ok := originalRules.([]any)
	if !ok {
		t.Fatal("allocator rules missing")
	}
	var reduced []any
	changed := false
	for _, value := range rules {
		rule, ok := value.(map[string]any)
		if !ok {
			t.Fatal("invalid allocator rule")
		}
		encoded, _ := json.Marshal(rule)
		var copy map[string]any
		if json.Unmarshal(encoded, &copy) != nil {
			t.Fatal("invalid allocator rule copy")
		}
		resources, _ := copy["resources"].([]any)
		groups, _ := copy["apiGroups"].([]any)
		if len(resources) == 1 && resources[0] == "namespaces" && len(groups) == 1 && groups[0] == "" {
			var verbs []any
			for _, verb := range copy["verbs"].([]any) {
				if verb == "delete" {
					changed = true
					continue
				}
				verbs = append(verbs, verb)
			}
			copy["verbs"] = verbs
		}
		reduced = append(reduced, copy)
	}
	if !changed {
		t.Fatal("allocator namespace delete rule not found")
	}
	sequence, allowed := 0, true
	patchRules := func(rules any, allow bool) {
		if os.Getenv("STEGO_TEST_ALLOCATED_FIXTURE") == "1" {
			if allowed == allow {
				return
			}
			sequence++
			requestAllocatorDeleteAccess(t, allow, sequence)
			allowed = allow
			return
		}
		patch, err := json.Marshal(map[string]any{"rules": rules})
		if err != nil {
			t.Fatal(err)
		}
		k.must(t, "", "patch", "clusterrole", role, "--type=merge", "-p", string(patch))
	}
	patchRules(reduced, false)
	defer patchRules(originalRules, true)
	allocatorClient := allocatorKube.workerClient(t)
	defer allocatorClient.Close()
	requireWorkerPermission(t, allocatorClient, "delete", "", "namespaces", "", false)
	a, err := allocation.New(allocatorClient, allocatorKube.options.ControlNamespace)
	if err != nil {
		t.Fatal(err)
	}
	stopAllocator, allocatorLogs = startDatabaseController(t, allocatorBinary, allocatorKube, rpcAddress, tlsIdentity.config.CAFile, allocatorToken, workerSettings...)
	stopController, logs = startDatabaseController(t, controllerBinary, k, rpcAddress, tlsIdentity.config.CAFile, controllerToken, workerSettings...)
	until = time.Now().Add(30 * time.Second)
	for !controllerRetryLogged(allocatorLogs()) {
		if time.Now().After(until) {
			t.Fatalf("allocator cleanup denial was lost\n%s", allocatorLogs())
		}
		time.Sleep(100 * time.Millisecond)
	}
	requireDatabaseDeleteDenied(t, func(ctx context.Context) error { _, err := a.Delete(ctx, "database", namespace, db.ID); return err })
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = provider.Delete(ctx, row)
	cancel()
	if !errors.Is(err, databasecontroller.ErrPending) {
		t.Fatal("database cleanup did not remain pending", err)
	}
	summary(1)
	k.must(t, "", "get", "namespace", namespace)
	patchRules(originalRules, true)
	requireWorkerPermission(t, allocatorClient, "delete", "", "namespaces", "", true)
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
