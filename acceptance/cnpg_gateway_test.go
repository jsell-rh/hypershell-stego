package acceptance

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/dynamicpb"
)

func cnpgGatewayName(id string) string {
	parsed, _ := ksuid.Parse(id)
	return "gw-" + hex.EncodeToString(parsed.Bytes())
}
func cnpgGatewayRole(id string) string { return strings.Replace(cnpgGatewayName(id), "gw-", "gw_", 1) }

func finishCNPGGateway(t *testing.T, k *kubeFixture, f *fixture, first httpapi.Gateway, ns, root, alice, controllerToken string, apiTLS testIdentity, rpcAddress string, identityProvider *keycloakFixture, call func(string, string, string) (*dynamicpb.Message, error), connect func(httpapi.Gateway), disconnect, stopWorkload func(), startWorkload func(...string), logs func() string) {
	t.Helper()
	primary := strings.TrimSpace(string(k.must(t, "", "-n", ns, "get", "cluster", "openshell-db", "-o", "jsonpath={.status.currentPrimary}")))
	sql := func(statement string) string {
		return strings.TrimSpace(string(k.must(t, "", "-n", ns, "exec", primary, "-c", "postgres", "--", "psql", "-U", "postgres", "-d", "postgres", "-v", "ON_ERROR_STOP=1", "-Atc", statement)))
	}
	stopWorkload()
	input, _ := json.Marshal(gateways.CreateRequest{Name: "second-cnpg-gateway", ClusterID: f.cluster, ReleaseID: f.release})
	code, body := requestJSON(t, "POST", root, alice, input)
	var second httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &second) != nil || second.DatabaseID != first.DatabaseID {
		t.Fatal("second shared Gateway placement", code, string(body))
	}
	t.Cleanup(func() {
		sandbox, _ := gatewayworkload.SandboxNamespace(second.ID)
		k.must(t, "", "delete", "namespace", second.Namespace, sandbox, "--ignore-not-found=true", "--wait=false")
		k.must(t, "", "delete", "clusterrole,clusterrolebinding", second.Namespace, "--ignore-not-found=true")
		k.must(t, "", "delete", "validatingadmissionpolicy,validatingadmissionpolicybinding", sandbox, "--ignore-not-found=true")
	})
	await := func(label string, check func() bool) {
		t.Helper()
		deadline := time.Now().Add(150 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatalf("%s did not finish\n%s", label, logs())
			}
			time.Sleep(time.Second)
		}
	}
	// SQL state can survive the loss of all Gateway Kubernetes records.
	// It must not cause new keys or an implicit adoption of that SQL identity.
	sql("CREATE ROLE " + cnpgGatewayRole(second.ID))
	sql("CREATE DATABASE " + cnpgGatewayRole(second.ID) + " OWNER " + cnpgGatewayRole(second.ID))
	startWorkload()
	await("SQL state blocks new keys", func() bool {
		code, body := requestJSON(t, "GET", root+"/"+second.ID, alice, nil)
		var row httpapi.Gateway
		return code == 200 && json.Unmarshal(body, &row) == nil && row.Status != nil && *row.Status == "WorkloadUnavailable"
	})
	for _, name := range []string{cnpgGatewayName(second.ID) + "-keys", cnpgGatewayName(second.ID) + "-credentials"} {
		if value := k.must(t, "", "-n", ns, "get", "secret", name, "--ignore-not-found=true", "-o", "name"); len(bytes.TrimSpace(value)) != 0 {
			t.Fatal("existing SQL state received new key or credential material")
		}
	}
	sql("DROP DATABASE " + cnpgGatewayRole(second.ID))
	sql("DROP ROLE " + cnpgGatewayRole(second.ID))
	await("second Gateway startup", func() bool {
		code, body := requestJSON(t, "GET", root+"/"+second.ID, alice, nil)
		var row httpapi.Gateway
		return code == 200 && json.Unmarshal(body, &row) == nil && row.Phase != nil && *row.Phase == "Running"
	})
	k.must(t, "", "-n", second.Namespace, "rollout", "status", "deployment/openshell-gateway", "--timeout=120s")
	firstKeys := k.must(t, "", "-n", ns, "get", "secret", cnpgGatewayName(first.ID)+"-keys", "-o", "jsonpath={.data}")
	secondKeys := k.must(t, "", "-n", ns, "get", "secret", cnpgGatewayName(second.ID)+"-keys", "-o", "jsonpath={.data}")
	if bytes.Equal(firstKeys, secondKeys) {
		t.Fatal("Gateways share encryption keys")
	}
	disconnect()
	connect(second)
	secondClient, _ := keycloak.GatewayClientID(second.ID)
	secondToken := identityProvider.browserLogin(t, secondClient, "alice")
	if _, err := call("GetProvider", secondToken, `{"name":"stored-provider"}`); status.Code(err) != codes.NotFound {
		t.Fatal("second Gateway can read first Gateway data", err)
	}
	if _, err := call("CreateProvider", secondToken, `{"provider":{"metadata":{"name":"second-provider"},"type":"openai","credentials":{"OPENAI_API_KEY":"second-private-value"}}}`); err != nil {
		t.Fatal("second Gateway write", err)
	}
	cnpgGatewaySQLIsolation(t, k, ns, first, second, "gateway-sql-isolation")
	// Change actual SQL state without changing a Kubernetes resource.
	sql("ALTER ROLE " + cnpgGatewayRole(first.ID) + " CREATEDB")
	await("direct SQL role repair", func() bool {
		return sql("SELECT rolcreatedb FROM pg_roles WHERE rolname='"+cnpgGatewayRole(first.ID)+"'") == "f"
	})
	// Authentication must also recover without changing the stored password.
	reloadMarker := func() string {
		return strings.TrimSpace(string(k.must(t, "", "-n", ns, "get", "cluster", "openshell-db", "-o", "jsonpath={.metadata.annotations.cnpg\\.io/reloadedAt}")))
	}
	sql("ALTER ROLE " + cnpgGatewayRole(first.ID) + " PASSWORD 'temporary-acceptance-password'")
	// CNPG can repair this before another controller reload is needed.
	// Require working credentials, independent of which pass repaired them.
	cnpgGatewaySQLIsolation(t, k, ns, first, second, "gateway-sql-password-repair")
	sql("GRANT pg_read_all_data TO " + cnpgGatewayRole(first.ID))
	await("SQL membership repair", func() bool {
		return sql("SELECT count(*) FROM pg_auth_members m JOIN pg_roles r ON r.oid=m.member WHERE r.rolname='"+cnpgGatewayRole(first.ID)+"'") == "0"
	})
	dump := k.must(t, "", "-n", ns, "exec", primary, "-c", "postgres", "--", "pg_dump", "-U", "postgres", "-d", cnpgGatewayRole(first.ID), "--data-only")
	if bytes.Contains(dump, []byte("acceptance-only-upstream-secret")) || !bytes.Contains(dump, []byte("stored-provider")) {
		t.Fatal("stored provider secret is not encrypted or provider data is absent")
	}
	generation := strings.TrimSpace(string(k.must(t, "", "-n", ns, "get", "cluster", "openshell-db", "-o", "jsonpath={.metadata.generation}")))
	stableReload := reloadMarker()
	time.Sleep(12 * time.Second)
	if after := strings.TrimSpace(string(k.must(t, "", "-n", ns, "get", "cluster", "openshell-db", "-o", "jsonpath={.metadata.generation}"))); after != generation {
		t.Fatal("stable Gateway resync changed shared Cluster generation", generation, after)
	}
	if reloadMarker() != stableReload {
		t.Fatal("stable SQL checks requested another operator reload")
	}
	stopWorkload()
	code, body = requestJSON(t, "DELETE", root+"/"+first.ID, alice, nil)
	if code != 204 {
		t.Fatal("offline CNPG Gateway deletion", code, string(body))
	}
	awaitQueueEmpty(t, f)
	if code, _ = requestJSON(t, "GET", root+"/"+first.ID, alice, nil); code != 404 {
		t.Fatal("deleted Gateway is visible", code)
	}
	// Deny SQL access first. No stale role status may complete cleanup.
	startWorkload("HYPERSHELL_CNPG_DIAL_ADDRESS=127.0.0.1:1")
	_, connection := grpcClient(t, rpcAddress, apiTLS)
	stateClient := control.NewGatewayIdentityServiceClient(connection)
	observed := func(want bool) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+controllerToken))
		state, err := stateClient.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: first.ID})
		if err != nil {
			return false
		}
		complete, found := state.GetCleanupTargets()["workload"].GetTargets()[f.cluster]
		return found && complete == want
	}
	complete := func() bool { return observed(true) }
	await("SQL role removal", func() bool {
		return sql("SELECT count(*) FROM pg_roles WHERE rolname='"+cnpgGatewayRole(first.ID)+"'") == "0"
	})
	if !observed(false) {
		t.Fatal("cleanup did not retain a pending observation without SQL evidence")
	}
	k.must(t, "", "-n", ns, "get", "secret", cnpgGatewayName(first.ID)+"-keys", "-o", "name")
	stopWorkload()
	route := k.cnpgSQLRoute(t, ns)
	startWorkload("HYPERSHELL_CNPG_DIAL_ADDRESS=" + route)
	await("CNPG cleanup observation", complete)
	for kind, names := range map[string][]string{"databases": {cnpgGatewayName(first.ID)}, "secrets": {cnpgGatewayName(first.ID) + "-credentials", cnpgGatewayName(first.ID) + "-keys"}, "configmaps": {cnpgGatewayName(first.ID) + "-key-identity"}} {
		for _, name := range names {
			if output := k.must(t, "", "-n", ns, "get", kind, name, "--ignore-not-found=true", "-o", "name"); len(bytes.TrimSpace(output)) != 0 {
				t.Fatal("cleanup retained owned resource", kind, name)
			}
		}
	}
	if sql("SELECT count(*) FROM pg_database WHERE datname='"+cnpgGatewayRole(first.ID)+"'") != "0" {
		t.Fatal("SQL database survived cleanup")
	}
	k.must(t, "", "-n", ns, "get", "cluster", "openshell-db", "-o", "name")
	if after := k.must(t, "", "-n", ns, "get", "secret", cnpgGatewayName(second.ID)+"-keys", "-o", "jsonpath={.data}"); !bytes.Equal(after, secondKeys) {
		t.Fatal("first cleanup changed second Gateway keys")
	}
	if _, err := call("GetProvider", secondToken, `{"name":"second-provider"}`); err != nil {
		t.Fatal("first cleanup damaged second Gateway", err)
	}
	stopWorkload()
	startWorkload("HYPERSHELL_CNPG_DIAL_ADDRESS=" + route)
	await("cleanup after controller restart", complete)
	// A late SQL role must reopen retained cleanup, including after restart.
	stopWorkload()
	sql("CREATE ROLE " + cnpgGatewayRole(first.ID))
	sql("CREATE TABLE public.acceptance_late_role(value integer)")
	sql("ALTER TABLE public.acceptance_late_role OWNER TO " + cnpgGatewayRole(first.ID))
	startWorkload("HYPERSHELL_CNPG_DIAL_ADDRESS=" + route)
	await("late SQL role reopens cleanup", func() bool { return observed(false) })
	stopWorkload()
	sql("DROP TABLE public.acceptance_late_role")
	startWorkload("HYPERSHELL_CNPG_DIAL_ADDRESS=" + route)
	await("late SQL role removal", func() bool {
		return sql("SELECT count(*) FROM pg_roles WHERE rolname='"+cnpgGatewayRole(first.ID)+"'") == "0"
	})
	await("late SQL cleanup completion", complete)
	t.Log("CNPG Gateway passed identity, stored encrypted data, restart, separate roles and databases, SQL drift repair, denied cleanup, and shared database retention")
}

func cnpgGatewaySQLIsolation(t *testing.T, k *kubeFixture, ns string, first, second httpapi.Gateway, podName string) {
	t.Helper()
	script := `for i in $(seq 1 30); do if psql -Atc 'SELECT 1' >/dev/null 2>&1; then break; fi; sleep 1; done
psql -v ON_ERROR_STOP=1 -Atc 'SELECT ssl FROM pg_stat_ssl WHERE pid=pg_backend_pid(); SELECT rolsuper OR rolcreatedb OR rolcreaterole OR rolreplication OR rolbypassrls FROM pg_roles WHERE rolname=current_user;'
if PGDATABASE=` + cnpgGatewayRole(second.ID) + ` psql -Atc 'SELECT 1' >/dev/null 2>&1; then echo 'cross Gateway database access succeeded';exit 1;fi
if PGDATABASE=openshell psql -Atc 'SELECT 1' >/dev/null 2>&1; then echo 'bootstrap database access succeeded';exit 1;fi
if PGSSLMODE=disable psql -Atc 'SELECT 1' >/dev/null 2>&1; then echo 'plaintext SQL succeeded';exit 1;fi`
	k.apply(t, map[string]any{"apiVersion": "v1", "kind": "Pod", "metadata": map[string]any{"name": podName, "namespace": ns}, "spec": map[string]any{"restartPolicy": "Never", "automountServiceAccountToken": false, "securityContext": map[string]any{"runAsNonRoot": true, "runAsUser": 26, "runAsGroup": 26, "seccompProfile": map[string]any{"type": "RuntimeDefault"}}, "containers": []any{map[string]any{"name": "sql", "image": databasecontroller.CNPGPostgresImage, "command": []string{"sh", "-ec", script}, "securityContext": map[string]any{"allowPrivilegeEscalation": false, "capabilities": map[string]any{"drop": []string{"ALL"}}}, "resources": map[string]any{"requests": map[string]string{"cpu": "10m", "memory": "32Mi"}, "limits": map[string]string{"cpu": "100m", "memory": "128Mi"}}, "env": []any{
		map[string]any{"name": "PGPASSWORD", "valueFrom": map[string]any{"secretKeyRef": map[string]string{"name": cnpgGatewayName(first.ID) + "-credentials", "key": "password"}}},
		map[string]string{"name": "PGUSER", "value": cnpgGatewayRole(first.ID)}, map[string]string{"name": "PGDATABASE", "value": cnpgGatewayRole(first.ID)}, map[string]string{"name": "PGHOST", "value": "openshell-db-rw." + ns + ".svc.cluster.local"}, map[string]string{"name": "PGSSLMODE", "value": "verify-full"}, map[string]string{"name": "PGSSLROOTCERT", "value": "/ca/ca.crt"}, map[string]string{"name": "PGCONNECT_TIMEOUT", "value": "3"}}, "volumeMounts": []any{map[string]any{"name": "ca", "mountPath": "/ca", "readOnly": true}}}}, "volumes": []any{map[string]any{"name": "ca", "secret": map[string]string{"secretName": "openshell-db-ca"}}}}})
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	output, err := k.command(ctx, "", "-n", ns, "wait", "pod/"+podName, "--for=jsonpath={.status.phase}=Succeeded", "--timeout=80s")
	logs := k.must(t, "", "-n", ns, "logs", podName)
	if err != nil || strings.TrimSpace(string(logs)) != "t\nf" {
		t.Fatalf("Gateway SQL isolation failed: %v\n%s\n%s", err, output, logs)
	}
}

// A private NodePort route survives normal PostgreSQL connection close and
// primary Pod replacement. TLS still verifies the original service DNS name.
func (k *kubeFixture) cnpgSQLRoute(t *testing.T, ns string) string {
	t.Helper()
	var source struct {
		Spec struct{ Selector map[string]string }
	}
	if json.Unmarshal(k.must(t, "", "-n", ns, "get", "service", "openshell-db-rw", "-o", "json"), &source) != nil || len(source.Spec.Selector) == 0 {
		t.Fatal("CNPG primary service has no selector")
	}
	k.apply(t, map[string]any{"apiVersion": "v1", "kind": "Service", "metadata": map[string]any{"name": "acceptance-sql-route", "namespace": ns}, "spec": map[string]any{"type": "NodePort", "selector": source.Spec.Selector, "ports": []any{map[string]any{"port": 5432, "targetPort": 5432, "protocol": "TCP"}}}})
	var route struct {
		Spec struct{ Ports []struct{ NodePort int } }
	}
	if json.Unmarshal(k.must(t, "", "-n", ns, "get", "service", "acceptance-sql-route", "-o", "json"), &route) != nil || len(route.Spec.Ports) != 1 || route.Spec.Ports[0].NodePort < 1 {
		t.Fatal("CNPG test route has no port")
	}
	var nodes struct {
		Items []struct {
			Status struct {
				Addresses []struct{ Type, Address string }
			}
		}
	}
	if json.Unmarshal(k.must(t, "", "get", "nodes", "-o", "json"), &nodes) != nil || len(nodes.Items) != 1 {
		t.Fatal("CNPG test requires one isolated node")
	}
	for _, address := range nodes.Items[0].Status.Addresses {
		ip := net.ParseIP(address.Address)
		if address.Type == "InternalIP" && ip != nil && ip.IsPrivate() && ip.To4() != nil {
			return net.JoinHostPort(ip.String(), strconv.Itoa(route.Spec.Ports[0].NodePort))
		}
	}
	t.Fatal("CNPG test node has no private IPv4 route")
	return ""
}
