package acceptance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/metadata"
)

type kubeFixture struct {
	config  string
	options databasecontroller.KubernetesOptions
}

func (k *kubeFixture) command(ctx context.Context, input string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "kubectl", append([]string{"--kubeconfig", k.config}, args...)...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	return cmd.CombinedOutput()
}
func (k *kubeFixture) must(t *testing.T, input string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	output, err := k.command(ctx, input, args...)
	if err != nil {
		t.Fatalf("Kubernetes %s: %v\n%s", args[0], err, output)
	}
	return output
}
func (k *kubeFixture) apply(t *testing.T, objects ...map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": objects})
	if err != nil {
		t.Fatal(err)
	}
	k.must(t, string(body), "apply", "-f", "-")
}
func kubernetesFixture(t *testing.T) *kubeFixture {
	t.Helper()
	config := os.Getenv("STEGO_TEST_KUBECONFIG")
	if config == "" {
		if os.Getenv("STEGO_REQUIRE_KUBERNETES") == "1" {
			t.Fatal("Kubernetes is required")
		}
		t.Skip("set STEGO_TEST_KUBECONFIG to an isolated kind cluster with cert-manager")
	}
	k := &kubeFixture{config: config}
	contextName := strings.TrimSpace(string(k.must(t, "", "config", "current-context")))
	if !strings.HasPrefix(contextName, "kind-stego-") {
		t.Fatal("database test requires a kind-stego- context")
	}
	name := "stego-db-" + uuid.NewString()[:8]
	meta := func(n string) map[string]any { return map[string]any{"name": n} }
	k.apply(t, map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": meta(name)}, map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": name, "namespace": name}},
		map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": meta(name), "rules": []any{
			map[string]any{"apiGroups": []string{""}, "resources": []string{"namespaces", "secrets", "configmaps", "persistentvolumeclaims", "services"}, "verbs": []string{"get", "create", "patch", "delete"}},
			map[string]any{"apiGroups": []string{""}, "resources": []string{"pods"}, "verbs": []string{"get"}},
			map[string]any{"apiGroups": []string{"postgresql.cnpg.io"}, "resources": []string{"clusters"}, "verbs": []string{"get", "create", "patch", "delete"}},
			map[string]any{"apiGroups": []string{"apps"}, "resources": []string{"deployments"}, "verbs": []string{"get", "create", "patch"}},
			map[string]any{"apiGroups": []string{"cert-manager.io"}, "resources": []string{"certificates"}, "verbs": []string{"get", "create", "patch"}},
		}}, map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": meta(name), "roleRef": map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": name}, "subjects": []any{map[string]any{"kind": "ServiceAccount", "name": name, "namespace": name}}},
		map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer", "metadata": meta(name + "-self"), "spec": map[string]any{"selfSigned": map[string]any{}}})
	t.Cleanup(func() {
		k.must(t, "", "delete", "clusterrole,clusterrolebinding,clusterissuer", name, "--ignore-not-found=true")
		k.must(t, "", "delete", "clusterissuer", name+"-self", "--ignore-not-found=true")
		k.must(t, "", "-n", "cert-manager", "delete", "certificate,secret", name, "--ignore-not-found=true")
		k.must(t, "", "delete", "namespace", name, "--wait=false", "--ignore-not-found=true")
	})
	k.apply(t, map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "Certificate", "metadata": map[string]any{"name": name, "namespace": "cert-manager"}, "spec": map[string]any{"isCA": true, "commonName": "STEGO isolated database test CA", "secretName": name, "issuerRef": map[string]any{"name": name + "-self", "kind": "ClusterIssuer"}, "privateKey": map[string]any{"algorithm": "ECDSA", "size": 256}}})
	k.must(t, "", "-n", "cert-manager", "wait", "certificate/"+name, "--for=condition=Ready", "--timeout=60s")
	k.apply(t, map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer", "metadata": meta(name), "spec": map[string]any{"ca": map[string]any{"secretName": name}}})
	directory := t.TempDir()
	caEncoded := strings.TrimSpace(string(k.must(t, "", "config", "view", "--raw", "--minify", "-o", "jsonpath={.clusters[0].cluster.certificate-authority-data}")))
	ca, err := base64.StdEncoding.DecodeString(caEncoded)
	if err != nil {
		t.Fatal(err)
	}
	token := k.must(t, "", "-n", name, "create", "token", name, "--duration=1h")
	caPath, tokenPath := filepath.Join(directory, "ca.pem"), filepath.Join(directory, "token")
	if err := os.WriteFile(caPath, ca, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, token, 0600); err != nil {
		t.Fatal(err)
	}
	address := strings.TrimSpace(string(k.must(t, "", "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")))
	k.options = databasecontroller.KubernetesOptions{ServerURL: address, CAFile: caPath, TokenFile: tokenPath, ClusterIssuer: name}
	return k
}
func startDatabaseController(t *testing.T, binary string, k *kubeFixture, address, ca, bearer string, settings ...string) (func(), func() string) {
	t.Helper()
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(bearer), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "DATABASE_PROVIDER=deployment", "HYPERSHELL_API_GRPC_ADDR="+address, "HYPERSHELL_API_CA_FILE="+ca, "HYPERSHELL_API_TOKEN_FILE="+file,
		"HYPERSHELL_KUBERNETES_URL="+k.options.ServerURL, "HYPERSHELL_KUBERNETES_CA_FILE="+k.options.CAFile, "HYPERSHELL_KUBERNETES_TOKEN_FILE="+k.options.TokenFile, "HYPERSHELL_DATABASE_CLUSTER_ISSUER="+k.options.ClusterIssuer)
	cmd.Env = append(cmd.Env, settings...)
	if raceEnabled {
		cmd.Env = append(cmd.Env, "GORACE=halt_on_error=1 exitcode=66")
	}
	output := &runtimeOutput{}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("database controller exit: %v\n%s", err, output.String())
			}
		case <-time.After(8 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Errorf("database controller did not stop\n%s", output.String())
		}
	}
	t.Cleanup(stop)
	return stop, output.String
}
func TestDatabaseWorkloadAndOfflineDeletion(t *testing.T) {
	k := kubernetesFixture(t)
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "DATABASE_PROVIDER=deployment", `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	settings = withCleanupGrants(t, settings, cleanupGrant("controller", "ManagedDatabase", "provider", ""))
	settings = withControllerWriteGrants(t, settings, databaseWriteGrant("controller", "deployment"))
	binary := buildApplication(t)
	controllerBinary := buildProgram(t, "./cmd/database-controller")
	stopAPI, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stopAPI() }()
	controllerToken := token(t, key, "controller")
	stopController, logs := startDatabaseController(t, controllerBinary, k, rpcAddress, tlsIdentity.config.CAFile, controllerToken)
	defer func() { stopController() }()
	creator, admin := token(t, key, "creator", "gateway:creator"), token(t, key, "operator", "platform:admin")
	input, _ := json.Marshal(gateways.CreateRequest{Name: "database-workflow", ClusterID: f.cluster, ReleaseID: f.release})
	code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", creator, input)
	for attempt := 0; code == 409 && attempt < 5; attempt++ {
		time.Sleep(100 * time.Millisecond)
		code, body = requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", creator, input)
	}

	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &gateway) != nil {
		t.Fatal("Gateway create", code, string(body))
	}
	namespace, err := gateways.DatabaseNamespace(gateway.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { k.must(t, "", "delete", "namespace", namespace, "--ignore-not-found=true", "--wait=false") })
	_, connection := grpcClient(t, rpcAddress, tlsIdentity)
	databases := pb.NewManagedDatabaseServiceClient(connection)
	call := func() context.Context {
		return metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+controllerToken))
	}
	deadline := time.Now().Add(150 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(call(), 3*time.Second)
		response, err := databases.GetManagedDatabase(ctx, &pb.GetManagedDatabaseRequest{Id: gateway.DatabaseID})
		cancel()
		if err == nil && response.GetManagedDatabase().GetStatus() == "ready" && response.ManagedDatabase.GetConnectionSecret() == databasecontroller.CredentialsName {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("database did not become ready: %v\n%s\n%s", err, logs(), k.must(t, "", "-n", namespace, "get", "pods", "-o", "wide"))
		}
		time.Sleep(time.Second)
	}
	secretBefore := strings.TrimSpace(string(k.must(t, "", "-n", namespace, "get", "secret", databasecontroller.CredentialsName, "-o", "jsonpath={.data.password}")))
	if secretBefore == "" {
		t.Fatal("empty password")
	}
	// Verify the CA and service DNS name, then write data over TLS through Service.
	sql := `export PGPASSWORD="$APP_PASSWORD" PGSSLMODE=verify-full PGSSLROOTCERT=/tls/ca.crt; psql -h openshell-gateway-db.` + namespace + `.svc.cluster.local -U openshell -d openshell -v ON_ERROR_STOP=1 -Atc 'SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid(); SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls FROM pg_roles WHERE rolname=current_user; CREATE TABLE acceptance_marker(value integer); INSERT INTO acceptance_marker VALUES (42);'`
	output := k.must(t, "", "-n", namespace, "exec", "deployment/openshell-gateway-db", "--", "sh", "-ec", sql)
	if !strings.HasPrefix(string(output), "t\nf\n") {
		t.Fatal("connection did not use TLS", string(output))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, err = k.command(ctx, "", "-n", namespace, "exec", "deployment/openshell-gateway-db", "--", "sh", "-ec", `PGPASSWORD="$APP_PASSWORD" PGSSLMODE=disable psql -h 127.0.0.1 -U openshell -d openshell -c 'SELECT 1'`)
	cancel()
	if err == nil {
		t.Fatal("unencrypted database connection succeeded")
	}
	stopController()
	stopController, logs = startDatabaseController(t, controllerBinary, k, rpcAddress, tlsIdentity.config.CAFile, controllerToken)
	k.must(t, "", "-n", namespace, "delete", "pod", "-l", "hypershell.redhat.io/database-id="+gateway.DatabaseID, "--wait=true")
	k.must(t, "", "-n", namespace, "wait", "pods", "-l", "hypershell.redhat.io/database-id="+gateway.DatabaseID, "--for=condition=Ready", "--timeout=90s")
	// Pod readiness does not wait for Service routing to reach the new Pod.
	// Retry this read only. The first successful result must contain the marker.
	readContext, stopRead := context.WithTimeout(context.Background(), 30*time.Second)
	defer stopRead()
	for {
		attempt, stopAttempt := context.WithTimeout(readContext, 5*time.Second)
		output, err = k.command(attempt, "", "-n", namespace, "exec", "deployment/openshell-gateway-db", "--", "sh", "-ec", `export PGPASSWORD="$APP_PASSWORD" PGSSLMODE=verify-full PGSSLROOTCERT=/tls/ca.crt PGCONNECT_TIMEOUT=2; psql -h openshell-gateway-db.`+namespace+`.svc.cluster.local -U openshell -d openshell -v ON_ERROR_STOP=1 -Atc 'SELECT value FROM acceptance_marker'`)
		stopAttempt()
		if err == nil {
			break
		}
		select {
		case <-readContext.Done():
			t.Fatalf("database Service read after restart did not recover: %v\n%s", err, output)
		case <-time.After(250 * time.Millisecond):
		}
	}
	if strings.TrimSpace(string(output)) != "42" {
		t.Fatal("persistent data changed", string(output))
	}
	secretAfter := strings.TrimSpace(string(k.must(t, "", "-n", namespace, "get", "secret", databasecontroller.CredentialsName, "-o", "jsonpath={.data.password}")))
	if secretAfter != secretBefore {
		t.Fatal("controller changed the password")
	}
	// A foreign namespace with the expected name must not be adopted or deleted.
	provider, err := databasecontroller.NewKubernetes(k.options)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	readyDatabase := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: gateway.DatabaseID}, Provider: "deployment", Namespace: namespace}
	versionBefore := strings.TrimSpace(string(k.must(t, "", "-n", namespace, "get", "deployment", databasecontroller.WorkloadName, "-o", "jsonpath={.metadata.resourceVersion}")))
	started := time.Now()
	for i := 0; i < 5; i++ {
		if err := provider.Ensure(context.Background(), readyDatabase); err != nil {
			t.Fatal("stable database reconciliation", err)
		}
	}
	t.Logf("Five stable reconciliations: %s", time.Since(started))
	versionAfter := strings.TrimSpace(string(k.must(t, "", "-n", namespace, "get", "deployment", databasecontroller.WorkloadName, "-o", "jsonpath={.metadata.resourceVersion}")))
	if versionAfter != versionBefore {
		t.Fatal("stable reconciliation changed the deployment")
	}

	foreignID := ksuid.New().String()
	foreignNS, _ := gateways.DatabaseNamespace(foreignID)
	k.must(t, "", "create", "namespace", foreignNS)
	t.Cleanup(func() { k.must(t, "", "delete", "namespace", foreignNS, "--wait=false", "--ignore-not-found=true") })
	foreign := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: foreignID}, Provider: "deployment", Namespace: foreignNS}
	if err := provider.Ensure(context.Background(), foreign); err == nil {
		t.Fatal("adopted a foreign namespace")
	}
	if err := provider.Delete(context.Background(), foreign); err == nil {
		t.Fatal("deleted a foreign namespace")
	}
	k.must(t, "", "get", "namespace", foreignNS)
	// Stop both consumers before deletion. Restart the API before the controller,
	// so retained database rows are the only source of the cleanup requirement.
	stopController()
	code, body = requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+gateway.ID, creator, nil)
	if code != 204 {
		t.Fatal("Gateway delete", code, string(body))
	}
	code, body = requestJSON(t, "DELETE", address+"/api/hypershell/v1/managed_databases/"+gateway.DatabaseID, admin, nil)
	if code != 204 {
		t.Fatal("database delete", code, string(body))
	}
	k.must(t, "", "get", "namespace", namespace)
	stopAPI()
	stopAPI, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	// Deny cleanup first. A failed DELETE must remain eligible for replay.
	role := k.options.ClusterIssuer
	k.must(t, "", "patch", "clusterrole", role, "--type=json", "-p", `[{"op":"replace","path":"/rules/0/verbs","value":["get","create","patch"]}]`)
	stopController, logs = startDatabaseController(t, controllerBinary, k, rpcAddress, tlsIdentity.config.CAFile, controllerToken)
	deadline = time.Now().Add(30 * time.Second)
	for !controllerRetryLogged(logs()) {
		if time.Now().After(deadline) {
			t.Fatalf("cleanup failure was not reported\n%s", logs())
		}
		time.Sleep(100 * time.Millisecond)
	}
	requireDatabaseDeleteDenied(t, func(ctx context.Context) error { return provider.Delete(ctx, readyDatabase) })
	k.must(t, "", "get", "namespace", namespace)
	var deniedComplete bool
	if err := f.db.QueryRow("SELECT (stego_cleanup->>'provider')::boolean FROM managed_databases WHERE id=$1", gateway.DatabaseID).Scan(&deniedComplete); err != nil || deniedComplete {
		t.Fatal("denied cleanup was recorded as complete", err)
	}
	k.must(t, "", "patch", "clusterrole", role, "--type=json", "-p", `[{"op":"replace","path":"/rules/0/verbs","value":["get","create","patch","delete"]}]`)
	deadline = time.Now().Add(90 * time.Second)
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		output, err := k.command(ctx, "", "get", "namespace", namespace, "--ignore-not-found=true", "-o", "name")
		cancel()
		if err == nil && strings.TrimSpace(string(output)) == "" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("offline delete was not replayed: %v\n%s", err, logs())
		}
		time.Sleep(time.Second)
	}
	awaitCleanup := func(want bool) {
		t.Helper()
		until := time.Now().Add(30 * time.Second)
		for {
			var complete bool
			if err := f.db.QueryRow("SELECT (stego_cleanup->>'provider')::boolean FROM managed_databases WHERE id=$1", gateway.DatabaseID).Scan(&complete); err != nil {
				t.Fatal(err)
			}
			if complete == want {
				return
			}
			if time.Now().After(until) {
				t.Fatalf("cleanup observation did not become %t\n%s", want, logs())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	awaitCleanup(true)
	stopController()
	// A late external effect must reopen cleanup after a recorded success.
	late, _ := json.Marshal(map[string]any{
		"apiVersion": "v1", "kind": "Namespace",
		"metadata": map[string]any{"name": namespace,
			"labels":     map[string]string{"hypershell.redhat.io/database-id": gateway.DatabaseID, "app.kubernetes.io/managed-by": "hypershell-database-controller"},
			"finalizers": []string{"acceptance.hypershell.test/hold"}},
	})
	k.must(t, string(late), "create", "-f", "-")
	stopController, logs = startDatabaseController(t, controllerBinary, k, rpcAddress, tlsIdentity.config.CAFile, controllerToken)
	awaitCleanup(false)
	k.must(t, "", "get", "namespace", namespace)
	stopController()
	stopController, logs = startDatabaseController(t, controllerBinary, k, rpcAddress, tlsIdentity.config.CAFile, controllerToken)
	k.must(t, "", "patch", "namespace", namespace, "--type=merge", "-p", `{"metadata":{"finalizers":[]}}`)
	k.must(t, "", "wait", "--for=delete", "namespace/"+namespace, "--timeout=90s")
	awaitCleanup(true)
	t.Log(fmt.Sprintf("Gateway %s: TLS database, persisted data, stable password, foreign namespace denial, offline cleanup, and late-effect cleanup passed", gateway.ID))
}

// Common controller logs contain fixed outcomes, not provider error text.
func controllerRetryLogged(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		var record struct {
			Event     string `json:"event.name"`
			Operation string `json:"operation"`
			Outcome   string `json:"outcome"`
			Retry     bool   `json:"retry"`
		}
		if json.Unmarshal([]byte(line), &record) == nil && record.Event == "controller.work.completed" && record.Operation == "reconcile" && record.Outcome == "failure" && record.Retry {
			return true
		}
	}
	return false
}

func requireDatabaseDeleteDenied(t *testing.T, remove func(context.Context) error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := remove(ctx)
	var failure *kube.APIError
	if !errors.As(err, &failure) || failure.Method != http.MethodDelete || failure.StatusCode != http.StatusForbidden {
		t.Fatal("database deletion did not return HTTP 403", err)
	}
}
