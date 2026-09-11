package acceptance

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	protocol "github.com/jsell-rh/hypershell-stego/contracts/gateway"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

const gatewayImage = "quay.io/opendatahub/odh-openshell-gateway:v0.0.109-rhaiv.0@sha256:a80b79e514826e8d57ea137749cf18a6e7f3d92e26bfefe005f3a9c4a55b8bdd"
const supervisorImage = "quay.io/opendatahub/odh-openshell-supervisor:v0.0.109-rhaiv.0@sha256:96e21135c18bc9f6f4d1dfd0cccae3c91769ef4d87da2e470eca4b56a24b2152"
const sandboxImage = "ghcr.io/nvidia/openshell-community/sandboxes/base@sha256:aeef1c63f00e2913ea002ccb3aaf925f338b5c5d70e63576f0d95c16a138044e"

func kindBridgeIP(t *testing.T) string {
	t.Helper()
	output, err := exec.Command("docker", "network", "inspect", "kind").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	var networks []struct {
		IPAM struct{ Config []struct{ Gateway string } }
	}
	if json.Unmarshal(output, &networks) != nil || len(networks) != 1 {
		t.Fatal("invalid kind network")
	}
	for _, config := range networks[0].IPAM.Config {
		ip := net.ParseIP(config.Gateway)
		if ip != nil && ip.To4() != nil && ip.IsPrivate() {
			return ip.String()
		}
	}
	t.Fatal("kind has no private IPv4 bridge address")
	return ""
}

func gatewayControllerRBAC(t *testing.T, k *kubeFixture) {
	t.Helper()
	var role map[string]any
	if json.Unmarshal(k.must(t, "", "get", "clusterrole", k.options.ClusterIssuer, "-o", "json"), &role) != nil {
		t.Fatal("read controller RBAC")
	}
	rules := role["rules"].([]any)
	for _, rule := range []map[string]any{
		{"apiGroups": []string{"postgresql.cnpg.io"}, "resources": []string{"databases"}, "verbs": []string{"get", "create", "patch", "delete"}},
		{"apiGroups": []string{"admissionregistration.k8s.io"}, "resources": []string{"validatingadmissionpolicies", "validatingadmissionpolicybindings", "mutatingadmissionpolicies", "mutatingadmissionpolicybindings"}, "verbs": []string{"get", "create", "patch", "delete"}},
		{"apiGroups": []string{"node.k8s.io"}, "resources": []string{"runtimeclasses"}, "verbs": []string{"get"}},
		{"apiGroups": []string{""}, "resources": []string{"namespaces"}, "verbs": []string{"list"}},
		{"apiGroups": []string{""}, "resources": []string{"serviceaccounts"}, "verbs": []string{"get", "create", "patch"}},
		{"apiGroups": []string{"rbac.authorization.k8s.io"}, "resources": []string{"roles", "rolebindings", "clusterroles", "clusterrolebindings"}, "verbs": []string{"get", "list", "create", "patch", "delete"}},
		{"apiGroups": []string{"authentication.k8s.io"}, "resources": []string{"tokenreviews"}, "verbs": []string{"create"}},
		{"apiGroups": []string{""}, "resources": []string{"nodes", "events"}, "verbs": []string{"get", "list", "watch"}},
		{"apiGroups": []string{""}, "resources": []string{"pods"}, "verbs": []string{"get", "create"}},
		{"apiGroups": []string{"agents.x-k8s.io"}, "resources": []string{"sandboxes", "sandboxes/status"}, "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}},
	} {
		rules = append(rules, rule)
	}
	role["rules"] = rules
	k.apply(t, role)
}

func (k *kubeFixture) forwardGateway(t *testing.T, ns string) (string, func()) {
	return k.forwardService(t, ns, "openshell-gateway", "8080")
}
func (k *kubeFixture) forwardService(t *testing.T, ns, service, port string) (string, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, "kubectl", "--kubeconfig", k.config, "-n", ns, "port-forward", "service/"+service, "0:"+port, "--address=127.0.0.1")
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	logs := &runtimeOutput{}
	cmd.Stderr = logs
	if err = cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	ready := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(output)
		for scanner.Scan() {
			line := scanner.Text()
			if strings.HasPrefix(line, "Forwarding from 127.0.0.1:") {
				select {
				case ready <- strings.Fields(line)[2]:
				default:
				}
			}
		}
		done <- cmd.Wait()
	}()
	var once sync.Once
	stop := func() { once.Do(func() { cancel(); <-done }) }
	t.Cleanup(stop)
	select {
	case address := <-ready:
		return address, stop
	case <-time.After(15 * time.Second):
		t.Fatalf("Gateway forwarding did not start: %s", logs.String())
	}
	return "", stop
}

func TestGatewayWorkloadWithDatabaseAndIdentity(t *testing.T) { testGatewayWorkload(t, false) }
func TestCNPGGatewayWorkloadWithDatabaseAndIdentity(t *testing.T) {
	if os.Getenv("STEGO_REQUIRE_CNPG") != "1" {
		t.Skip("run scripts/check-workload.sh cnpg-gateway")
	}
	testGatewayWorkload(t, true)
}
func testGatewayWorkload(t *testing.T, cnpg bool) {
	provider := "deployment"
	if cnpg {
		provider = "cnpg"
	}

	k := kubernetesFixture(t)
	gatewayControllerRBAC(t, k)
	identityProvider := startKeycloakAt(t, kindBridgeIP(t), func(realm map[string]any) { realm["accessTokenLifespan"] = 900 })
	settings, _ := identityProvider.apiLoginSetup(t)
	aliceID := identityProvider.human(t, "alice")
	bobSubject := identityProvider.human(t, "bob")
	controllerID := identityProvider.human(t, "controller")
	response := identityProvider.adminRequest(t, "GET", "/clients?clientId=hypershell", nil)
	var clients []struct{ ID string }
	if json.Unmarshal(response.Body, &clients) != nil || len(clients) != 1 {
		t.Fatal("find API client")
	}
	response = identityProvider.adminRequest(t, "GET", "/clients/"+clients[0].ID+"/roles/gateway:creator", nil)
	var role map[string]any
	if json.Unmarshal(response.Body, &role) != nil {
		t.Fatal("read creator role")
	}
	identityProvider.adminRequest(t, "POST", "/users/"+aliceID+"/role-mappings/clients/"+clients[0].ID, []any{role})
	alice := identityProvider.browserLogin(t, "hypershell", "alice")
	controllerToken := identityProvider.browserLogin(t, "hypershell", "controller")
	f := database(t)
	if _, err := f.db.Exec("UPDATE gateway_releases SET image=$1 WHERE id=$2", gatewayImage, f.release); err != nil {
		t.Fatal(err)
	}
	apiTLS := identity(t, "localhost")
	dir := filepath.Dir(apiTLS.config.CAFile)
	allowed, _ := json.Marshal([]string{controllerID})
	settings = append(settings, "DATABASE_PROVIDER="+provider, "HYPERSHELL_CONTROL_PLANE_SUBJECTS="+string(allowed), "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"))
	settings = withCleanupGrants(t, settings, cleanupGrant(controllerID, "ManagedDatabase", "provider", ""), cleanupGrant(controllerID, "Gateway", "identity", ""), cleanupGrant(controllerID, "Gateway", "workload", f.cluster))
	settings = withControllerWriteGrants(t, settings, writeGrant(controllerID, "configure.identity", ""), writeGrant(controllerID, "observe.workload", f.cluster), databaseWriteGrant(controllerID, provider))
	accountKey, accountAuth := issuer(t)
	accountSettings, stopAccountProvider := startRealProvisioner(t, identityProvider, accountKey, accountAuth)
	defer stopAccountProvider()
	settings = append(settings, accountSettings...)
	_, config := broker(t, identity(t, "localhost"))
	apiBinary := buildApplication(t)
	stopAPI, address, rpcAddress := startBoth(t, apiBinary, f.dsn, config, settings...)
	dbBinary := buildProgram(t, "./cmd/database-controller")
	stopDatabase, _ := startDatabaseController(t, dbBinary, k, rpcAddress, apiTLS.config.CAFile, controllerToken, "DATABASE_PROVIDER="+provider)
	identityBinary := buildProgram(t, "./out/deploy/workers/gateway-identity")
	stopIdentity, _ := startIdentityController(t, identityBinary, identityProvider, rpcAddress, apiTLS.config.CAFile, controllerToken)
	workloadBinary := buildProgram(t, "./cmd/gateway-workload-controller")
	workloadSettings := []string{"HYPERSHELL_MANAGED_CLUSTER_ID=" + f.cluster, "HYPERSHELL_GATEWAY_CLUSTER_ISSUER=" + k.options.ClusterIssuer, "HYPERSHELL_GATEWAY_OIDC_ISSUER=" + identityProvider.options.ServerURL + "/realms/workflow", "HYPERSHELL_GATEWAY_TRUST_BUNDLE=" + identityProvider.options.CAFile, "HYPERSHELL_GATEWAY_SANDBOX_IMAGE=" + sandboxImage, "HYPERSHELL_GATEWAY_SUPERVISOR_IMAGE=" + supervisorImage}
	workloadSettings = append(workloadSettings, "HYPERSHELL_GATEWAY_SANDBOX_RUNTIME_CLASS="+os.Getenv("STEGO_TEST_SANDBOX_RUNTIME_CLASS"))
	if cnpg {
		ns, _ := gateways.DatabaseNamespace(f.database)
		k.must(t, "", "-n", ns, "wait", "cluster/openshell-db", "--for=create", "--timeout=80s")
		k.must(t, "", "-n", ns, "wait", "cluster/openshell-db", "--for=condition=Ready", "--timeout=80s")
		route := k.cnpgSQLRoute(t, ns)
		workloadSettings = append(workloadSettings, "HYPERSHELL_CNPG_DIAL_ADDRESS="+route)
	}
	stopWorkload, logs := startDatabaseController(t, workloadBinary, k, rpcAddress, apiTLS.config.CAFile, controllerToken, workloadSettings...)
	input, _ := json.Marshal(gateways.CreateRequest{Name: "actual-gateway", ClusterID: f.cluster, ReleaseID: f.release})
	root := address + "/api/hypershell/v1/gateways"
	code, body := requestJSON(t, "POST", root, alice, input)
	for attempt := 0; code == 409 && attempt < 5; attempt++ {
		time.Sleep(100 * time.Millisecond)
		code, body = requestJSON(t, "POST", root, alice, input)
	}
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &gateway) != nil {
		t.Fatal("Gateway creation", code, string(body))
	}
	checkCount, recoverCount := startSandboxCountWorkflow(t, k, address, rpcAddress, apiTLS, controllerToken, alice, f.cluster, gateway)
	checkCount(0)
	dbNamespace, err := gateways.DatabaseNamespace(gateway.DatabaseID)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stopWorkload()
		stopIdentity()
		stopDatabase()
		stopAPI()
		sandboxNS, _ := gatewayworkload.SandboxNamespace(gateway.ID)
		k.must(t, "", "delete", "namespace", sandboxNS, "--ignore-not-found=true", "--wait=true")
		k.must(t, "", "delete", "validatingadmissionpolicy,validatingadmissionpolicybinding", sandboxNS, "--ignore-not-found=true")
		if os.Getenv("STEGO_TEST_SANDBOX_RUNTIME_CLASS") != "" {
			k.must(t, "", "delete", "mutatingadmissionpolicy,mutatingadmissionpolicybinding", sandboxNS, "--ignore-not-found=true")
		}
		k.must(t, "", "delete", "namespace", gateway.Namespace, dbNamespace, "--ignore-not-found=true", "--wait=false")
		k.must(t, "", "delete", "clusterrole,clusterrolebinding", gateway.Namespace, "--ignore-not-found=true")
	})
	ready := func() {
		t.Helper()
		deadline := time.Now().Add(180 * time.Second)
		for {
			code, body := requestJSON(t, "GET", root+"/"+gateway.ID, alice, nil)
			var current httpapi.Gateway
			if code == 200 && json.Unmarshal(body, &current) == nil && current.Status != nil && *current.Status == "Healthy" && current.Phase != nil && *current.Phase == "Running" {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				body, err := k.command(ctx, "", "-n", gateway.Namespace, "get", "deployment/openshell-gateway", "-o", "json")
				cancel()
				var deployment struct {
					Metadata struct{ Generation int64 }
					Status   struct {
						ObservedGeneration             int64
						ReadyReplicas, UpdatedReplicas int
					}
				}
				if err == nil && json.Unmarshal(body, &deployment) == nil && deployment.Metadata.Generation > 0 && deployment.Status.ObservedGeneration >= deployment.Metadata.Generation && deployment.Status.ReadyReplicas == 1 && deployment.Status.UpdatedReplicas == 1 {
					return
				}
			}
			if time.Now().After(deadline) {
				pods, _ := k.command(context.Background(), "", "-n", gateway.Namespace, "get", "pods", "-o", "wide")
				appLogs, _ := k.command(context.Background(), "", "-n", gateway.Namespace, "logs", "deployment/openshell-gateway", "--tail=35")
				t.Fatalf("Gateway did not become ready: %s\n%s\n%s", logs(), pods, appLogs)
			}
			time.Sleep(time.Second)
		}
	}
	ready()
	keyData := func(namespace string) []byte {
		t.Helper()
		name := "openshell-gateway-keys"
		if cnpg && namespace == dbNamespace {
			parsed, _ := ksuid.Parse(gateway.ID)
			name = "gw-" + hex.EncodeToString(parsed.Bytes()) + "-keys"
		}
		return k.must(t, "", "-n", namespace, "get", "secret/"+name, "-o", "jsonpath={.data}")
	}
	originalKeys := keyData(dbNamespace)
	if !bytes.Equal(originalKeys, keyData(gateway.Namespace)) {
		t.Fatal("Gateway key copy differs from its durable source")
	}
	gatewayClient, _ := keycloak.GatewayClientID(gateway.ID)
	ownerToken := identityProvider.browserLogin(t, gatewayClient, "alice")
	bobToken := identityProvider.browserLogin(t, gatewayClient, "bob")
	service, err := protocol.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var connection *grpc.ClientConn
	var stopForward func()
	connect := func(target httpapi.Gateway) {
		t.Helper()
		forward, stop := k.forwardGateway(t, target.Namespace)
		stopForward = stop
		encoded := strings.TrimSpace(string(k.must(t, "", "-n", target.Namespace, "get", "secret", "openshell-server-tls", "-o", "jsonpath={.data.ca\\.crt}")))
		ca, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM(ca) {
			t.Fatal("Gateway CA is invalid")
		}
		connection, err = grpc.NewClient("passthrough:///"+forward, grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, RootCAs: roots, ServerName: "openshell-gateway." + target.Namespace + ".svc.cluster.local"})), grpc.WithDisableRetry(), grpc.WithDisableServiceConfig(), grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(64<<10), grpc.MaxCallSendMsgSize(64<<10)))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { connection.Close() })
	}
	connect(gateway)
	call := func(method, bearer, input string) (*dynamicpb.Message, error) {
		t.Helper()
		descriptor := service.Methods().ByName(protoreflect.Name(method))
		if descriptor == nil {
			t.Fatal("missing Gateway method", method)
		}
		request := dynamicpb.NewMessage(descriptor.Input())
		if err := protojson.Unmarshal([]byte(input), request); err != nil {
			t.Fatal(err)
		}
		response := dynamicpb.NewMessage(descriptor.Output())
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if bearer != "" {
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
		}
		err := connection.Invoke(ctx, "/openshell.v1.OpenShell/"+method, request, response)
		return response, err
	}
	for _, bearer := range []string{"", "forged", alice} {
		if _, err := call("GetCurrentUser", bearer, `{}`); status.Code(err) != codes.Unauthenticated {
			t.Fatalf("Gateway accepted an invalid identity: %v", err)
		}
	}
	if _, err := call("GetCurrentUser", ownerToken, `{}`); err != nil {
		t.Fatal("owner identity", err)
	}
	if _, err := call("GetProvider", bobToken, `{"name":"stored-provider"}`); status.Code(err) != codes.PermissionDenied {
		t.Fatal("ungranted user reached Gateway data", err)
	}
	if _, err := call("CreateProvider", ownerToken, `{"provider":{"metadata":{"name":"stored-provider"},"type":"openai","credentials":{"OPENAI_API_KEY":"acceptance-only-upstream-secret"},"config":{"acceptance":"persisted"}}}`); err != nil {
		t.Fatal("create Gateway provider", err)
	}
	before, err := call("GetProvider", ownerToken, `{"name":"stored-provider"}`)
	if err != nil {
		t.Fatal(err)
	}
	var finishSandbox func(*grpc.ClientConn, string)
	if os.Getenv("STEGO_TEST_SANDBOX_RUNTIME_CLASS") != "" {
		finishSandbox = gatewaySandboxWorkflow(t, k, gateway, service, connection, ownerToken, bobToken, call, checkCount)
		recoverCount()
	}
	checkViewerAfterRestart := startGatewayViewerWorkflow(t, identityProvider, address+"/api/hypershell/v1", gateway, gatewayClient, alice, ownerToken, bobSubject, before, call)
	stopWorkload()
	connection.Close()
	stopForward()
	k.must(t, "", "-n", gateway.Namespace, "delete", "pod", "-l", "hypershell.redhat.io/gateway-id="+gateway.ID, "--wait=true")
	stopWorkload, logs = startDatabaseController(t, workloadBinary, k, rpcAddress, apiTLS.config.CAFile, controllerToken, workloadSettings...)
	k.must(t, "", "-n", gateway.Namespace, "rollout", "status", "deployment/openshell-gateway", "--timeout=120s")
	ready()
	connect(gateway)
	after, err := call("GetProvider", ownerToken, `{"name":"stored-provider"}`)
	if err != nil || !proto.Equal(before, after) {
		t.Fatal("Gateway restart changed stored provider", err)
	}
	stopWorkload()
	connection.Close()
	stopForward()
	if cnpg {
		primary := strings.TrimSpace(string(k.must(t, "", "-n", dbNamespace, "get", "cluster", "openshell-db", "-o", "jsonpath={.status.currentPrimary}")))
		k.must(t, "", "-n", dbNamespace, "delete", "pod", primary, "--grace-period=30", "--wait=true")
		k.must(t, "", "-n", dbNamespace, "wait", "pod/"+primary, "--for=create", "--timeout=90s")
		k.must(t, "", "-n", dbNamespace, "wait", "pod/"+primary, "--for=condition=Ready", "--timeout=120s")
	} else {
		k.must(t, "", "-n", dbNamespace, "delete", "pod", "-l", "hypershell.redhat.io/database-id="+gateway.DatabaseID, "--wait=true")
		k.must(t, "", "-n", dbNamespace, "rollout", "status", "deployment/openshell-gateway-db", "--timeout=120s")
	}
	k.must(t, "", "delete", "namespace", gateway.Namespace, "--wait=true", "--timeout=90s")
	stopWorkload, logs = startDatabaseController(t, workloadBinary, k, rpcAddress, apiTLS.config.CAFile, controllerToken, workloadSettings...)
	ready()
	if !bytes.Equal(originalKeys, keyData(dbNamespace)) || !bytes.Equal(originalKeys, keyData(gateway.Namespace)) {
		t.Fatal("namespace replacement changed Gateway keys")
	}
	ownerToken = identityProvider.browserLogin(t, gatewayClient, "alice")
	connect(gateway)
	after, err = call("GetProvider", ownerToken, `{"name":"stored-provider"}`)
	if err != nil || !proto.Equal(before, after) {
		t.Fatal("namespace or database restart changed stored provider", err)
	}
	checkViewerAfterRestart(ownerToken)
	if finishSandbox != nil {
		finishSandbox(connection, ownerToken)
	}
	if cnpg {
		finishCNPGGateway(t, k, f, gateway, dbNamespace, root, alice, controllerToken, apiTLS, rpcAddress, identityProvider, call, connect, func() { connection.Close(); stopForward() }, func() { stopWorkload() }, func(extra ...string) {
			workloadSettings = append(workloadSettings, extra...)
			stopWorkload, logs = startDatabaseController(t, workloadBinary, k, rpcAddress, apiTLS.config.CAFile, controllerToken, workloadSettings...)
			// Retained completion can already be true. Require the new process
			// to scan before a restart check can return and stop it again.
			deadline := time.Now().Add(15 * time.Second)
			for !strings.Contains(logs(), `"operation":"scan","outcome":"success"`) {
				if time.Now().After(deadline) {
					t.Fatalf("restarted Gateway controller did not scan: %s", logs())
				}
				time.Sleep(20 * time.Millisecond)
			}
		}, func() string { return logs() })
		return
	}
	checkDeletedAccounts := gatewayAccountWorkflow(t, identityProvider, address, gateway.ID, alice, call)
	stopWorkload()
	connection.Close()
	stopForward()
	foreignID := ksuid.New().String()
	if err := f.storage.Create(context.Background(), "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: foreignID}, Name: "other-cluster", Provider: "kubernetes", KubeconfigSecret: "unused-target"}); err != nil {
		t.Fatal(err)
	}
	patch, _ := json.Marshal(map[string]string{"cluster_id": foreignID})
	code, body = requestJSON(t, "PATCH", root+"/"+gateway.ID, alice, patch)
	if code != 200 {
		t.Fatal("change cluster assignment before deletion", code, string(body))
	}
	if _, err := f.db.Exec(`ALTER TABLE managed_databases ADD CONSTRAINT acceptance_keep_database CHECK (deleted_at IS NULL)`); err != nil {
		t.Fatal(err)
	}
	code, body = requestJSON(t, "DELETE", root+"/"+gateway.ID, alice, nil)
	if code != 204 {
		t.Fatal("Gateway deletion", code, string(body))
	}
	checkDeletedAccounts()
	stopWorkload, logs = startDatabaseController(t, workloadBinary, k, rpcAddress, apiTLS.config.CAFile, controllerToken, workloadSettings...)
	absent := func(kind, name string) {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			output, err := k.command(ctx, "", "get", kind, name, "--ignore-not-found=true", "-o", "name")
			cancel()
			if err == nil && len(bytes.TrimSpace(output)) == 0 {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("offline Gateway deletion did not remove %s: %v\n%s", kind, err, logs())
			}
			time.Sleep(time.Second)
		}
	}
	sandboxNS, _ := gatewayworkload.SandboxNamespace(gateway.ID)
	absent("namespace", sandboxNS)
	absent("validatingadmissionpolicybinding", sandboxNS)
	absent("validatingadmissionpolicy", sandboxNS)
	if os.Getenv("STEGO_TEST_SANDBOX_RUNTIME_CLASS") != "" {
		absent("mutatingadmissionpolicybinding", sandboxNS)
		absent("mutatingadmissionpolicy", sandboxNS)
	}
	absent("namespace", gateway.Namespace)
	absent("clusterrolebinding", gateway.Namespace)
	absent("clusterrole", gateway.Namespace)
	deadline := time.Now().Add(30 * time.Second)
	for !controllerRetryLogged(logs()) {
		if time.Now().After(deadline) {
			t.Fatalf("database cleanup failure was not reported: %s", logs())
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, failedCleanupConnection := grpcClient(t, rpcAddress, apiTLS)
	failedCleanupContext, cancelFailedCleanup := context.WithTimeout(context.Background(), 5*time.Second)
	failedCleanupContext = metadata.NewOutgoingContext(failedCleanupContext, metadata.Pairs("authorization", "Bearer "+controllerToken))
	_, cleanupError := pb.NewManagedDatabaseServiceClient(failedCleanupConnection).DeleteManagedDatabase(failedCleanupContext, &pb.DeleteManagedDatabaseRequest{Id: gateway.DatabaseID})
	cancelFailedCleanup()
	failedCleanupConnection.Close()
	if status.Code(cleanupError) != codes.Internal {
		t.Fatal("database cleanup did not return Internal", cleanupError)
	}
	var retainedDatabase bool
	if err := f.db.QueryRow("SELECT deleted_at IS NULL FROM managed_databases WHERE id=$1", gateway.DatabaseID).Scan(&retainedDatabase); err != nil || !retainedDatabase {
		t.Fatal("failed cleanup removed its database", err)
	}
	k.must(t, "", "get", "namespace", dbNamespace, "-o", "name")
	if _, err := f.db.Exec(`ALTER TABLE managed_databases DROP CONSTRAINT acceptance_keep_database`); err != nil {
		t.Fatal(err)
	}
	absent("namespace", dbNamespace)
	_, cleanupConnection := grpcClient(t, rpcAddress, apiTLS)
	cleanupState := control.NewGatewayIdentityServiceClient(cleanupConnection)
	awaitTarget := func(want bool) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			ctx = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+controllerToken))
			state, err := cleanupState.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: gateway.ID})
			cancel()
			if err == nil {
				targets := state.GetCleanupTargets()["workload"].GetTargets()
				old, oldFound := targets[f.cluster]
				current, currentFound := targets[foreignID]
				if len(targets) == 2 && oldFound && currentFound && old == want && !current && !state.Cleanup["workload"] {
					return
				}
			}
			if time.Now().After(deadline) {
				t.Fatalf("cluster cleanup observation did not converge: %v\n%s", err, logs())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	awaitTarget(true)
	stopWorkload()
	late, _ := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": map[string]any{"name": gateway.Namespace, "labels": map[string]string{"hypershell.redhat.io/gateway-id": gateway.ID, "app.kubernetes.io/managed-by": "hypershell-gateway-controller"}, "finalizers": []string{"acceptance.hypershell.test/hold"}}})
	k.must(t, string(late), "create", "-f", "-")
	stopWorkload, logs = startDatabaseController(t, workloadBinary, k, rpcAddress, apiTLS.config.CAFile, controllerToken, workloadSettings...)
	awaitTarget(false)
	stopWorkload()
	stopWorkload, logs = startDatabaseController(t, workloadBinary, k, rpcAddress, apiTLS.config.CAFile, controllerToken, workloadSettings...)
	k.must(t, "", "patch", "namespace", gateway.Namespace, "--type=merge", "-p", `{"metadata":{"finalizers":[]}}`)
	absent("namespace", gateway.Namespace)
	awaitTarget(true)
	stopWorkload()
	t.Log("Former-cluster cleanup survived restart, reopened for a late namespace, and kept the new cluster pending")
	t.Logf("Gateway %s: verified database and OIDC, owner access, denial, provider persistence, Pod and database restart, namespace replacement, stable keys, and offline deletion passed", gateway.ID)
}
