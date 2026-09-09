package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	"github.com/jsell-rh/hypershell-stego/out/auth"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func startIdentityController(t *testing.T, binary string, k *keycloakFixture, address, ca, bearer string) (func(), func() string) {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "controller-token")
	if err := os.WriteFile(tokenFile, []byte(bearer), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(binary)
	command.Env = append(os.Environ(), "HYPERSHELL_API_GRPC_ADDR="+address, "HYPERSHELL_API_CA_FILE="+ca, "HYPERSHELL_API_TOKEN_FILE="+tokenFile,
		"HYPERSHELL_KEYCLOAK_URL="+k.options.ServerURL, "HYPERSHELL_KEYCLOAK_REALM="+k.options.Realm, "HYPERSHELL_KEYCLOAK_CLIENT_ID="+k.options.ClientID, "HYPERSHELL_KEYCLOAK_SECRET_FILE="+k.options.SecretFile, "HYPERSHELL_KEYCLOAK_CA_FILE="+k.options.CAFile)
	if raceEnabled {
		command.Env = append(command.Env, "GORACE=halt_on_error=1 exitcode=66")
	}
	output := &runtimeOutput{}
	command.Stdout = output
	command.Stderr = output
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = command.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("identity controller exit: %v\n%s", err, output.String())
			}
		case <-time.After(8 * time.Second):
			_ = command.Process.Kill()
			<-done
			t.Errorf("identity controller did not stop\n%s", output.String())
		}
	}
	t.Cleanup(stop)
	deadline := time.Now().Add(8 * time.Second)
	for !strings.Contains(output.String(), "Gateway identity watch started") || !strings.Contains(output.String(), "Gateway identity scan completed") {
		select {
		case err := <-done:
			stopped = true
			t.Fatalf("identity controller did not start: %v\n%s", err, output.String())
		default:
		}
		if time.Now().After(deadline) {
			stop()
			t.Fatalf("identity controller did not complete the initial scan\n%s", output.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	return stop, output.String
}
func (k *keycloakFixture) gatewayClient(t *testing.T, id string) map[string]any {
	t.Helper()
	clientID, err := keycloak.GatewayClientID(id)
	if err != nil {
		t.Fatal(err)
	}
	response, grant := k.issue(t, "provisioner", "acceptance-only-admin-secret")
	if response.StatusCode != 200 {
		t.Fatal("fixture administrator token failed")
	}
	headers := http.Header{"Authorization": {"Bearer " + grant["access_token"].(string)}}
	response, err = k.http.Do(context.Background(), "GET", "/admin/realms/workflow/clients?clientId="+url.QueryEscape(clientID), headers, nil)
	var clients []map[string]any
	if err != nil || response.StatusCode != 200 || json.Unmarshal(response.Body, &clients) != nil {
		t.Fatal("read Gateway provider client")
	}
	if len(clients) == 0 {
		return nil
	}
	if len(clients) != 1 {
		t.Fatal("duplicate Gateway provider client")
	}
	return clients[0]
}

func TestGatewayIdentityControllerWorkflow(t *testing.T) {
	k := startKeycloak(t)
	f := database(t)
	key, settings := issuer(t)
	providerSettings, _ := startRealProvisioner(t, k, key, settings)
	settings = append(settings, providerSettings...)
	tlsIdentity := identity(t, "localhost")
	dir := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["gateway-controller"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"))
	settings = withCleanupGrants(t, settings, cleanupGrant("gateway-controller", "Gateway", "identity", ""))
	settings = withControllerWriteGrants(t, settings, writeGrant("gateway-controller", "configure.identity", ""))
	_, config := broker(t, identity(t, "localhost"))
	apiBinary := buildApplication(t)
	controllerBinary := buildProgram(t, "./cmd/gateway-identity-controller")
	stopAPI, address, grpcAddress := startBoth(t, apiBinary, f.dsn, config, settings...)
	root := address + "/api/hypershell/v1/gateways"
	owner := token(t, key, "alice", "gateway:creator")
	controllerToken := token(t, key, "gateway-controller")
	body, err := json.Marshal(f.request("seed-identity"))
	if err != nil {
		t.Fatal(err)
	}
	code, body := requestJSON(t, "POST", root, owner, body)
	var created struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(body, &created) != nil || created.ID == "" {
		t.Fatalf("create seed Gateway: %d", code)
	}
	stopController, logs := startIdentityController(t, controllerBinary, k, grpcAddress, tlsIdentity.config.CAFile, controllerToken)
	waitOIDC := func(id string) string {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		expected, _ := keycloak.GatewayClientID(id)
		for {
			code, body := requestJSON(t, "GET", root+"/"+id, owner, nil)
			var gateway struct {
				OIDC *string `json:"oidc"`
			}
			var oidc map[string]any
			if code == 200 && json.Unmarshal(body, &gateway) == nil && gateway.OIDC != nil && json.Unmarshal([]byte(*gateway.OIDC), &oidc) == nil && oidc["client_id"] == expected {
				return *gateway.OIDC
			}
			if time.Now().After(deadline) {
				if live := k.gatewayClient(t, id); live != nil {
					for _, field := range []string{"attributes", "publicClient", "standardFlowEnabled", "defaultClientScopes", "optionalClientScopes", "redirectUris", "webOrigins"} {
						t.Logf("Gateway provider field %s: %v", field, live[field])
					}
				}
				t.Fatalf("controller did not set Gateway identity\n%s", logs())
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	firstOIDC := waitOIDC(created.ID)
	client, connection := grpcClient(t, grpcAddress, tlsIdentity)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	ownerContext := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+owner))
	controlContext := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+controllerToken))
	states := control.NewGatewayIdentityServiceClient(connection)
	if _, err := states.GetGatewayIdentityState(ownerContext, &control.GetGatewayIdentityStateRequest{Id: created.ID}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("owner read control-plane state: %v", err)
	}
	second, err := client.CreateGateway(ownerContext, &pb.CreateGatewayRequest{Name: "watch-identity", ClusterId: f.cluster, ReleaseId: f.release, DatabaseId: f.database})
	if err != nil {
		t.Fatal(err)
	}
	secondID := second.GetGateway().GetMetadata().GetId()
	waitOIDC(secondID)
	live := k.gatewayClient(t, created.ID)
	attributes, ok := live["attributes"].(map[string]any)
	if !ok || attributes["hypershell.gateway-id"] != created.ID || attributes["hypershell.gateway"] != "true" || attributes["pkce.code.challenge.method"] != "S256" || attributes["oauth2.device.authorization.grant.enabled"] != "true" {
		t.Fatal("controller did not create a trusted browser and device binding")
	}
	for _, field := range []string{"directAccessGrantsEnabled", "implicitFlowEnabled", "serviceAccountsEnabled", "fullScopeAllowed"} {
		if live[field] != false {
			t.Fatalf("Gateway client enabled %s", field)
		}
	}
	if live["publicClient"] != true || live["standardFlowEnabled"] != true || live["enabled"] != true {
		t.Fatal("Gateway browser client is not enabled")
	}
	var grants int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE gateway_id=$1 AND deleted_at IS NULL", created.ID).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("Gateway owner grant was not retained")
	}
	if code, _ := requestJSON(t, "GET", root+"/"+created.ID, token(t, key, "bob"), nil); code != 404 {
		t.Fatal("foreign Gateway access was permitted")
	}
	// Workload health is an explicit fixture. The identity controller must not set it.
	code, body = requestJSON(t, "GET", root+"/"+created.ID, owner, nil)
	var health struct {
		Status string `json:"status"`
		Phase  string `json:"phase"`
	}
	if code != 200 || json.Unmarshal(body, &health) != nil || health.Status == "Healthy" || health.Phase == "Running" {
		t.Fatal("identity controller claimed workload health")
	}
	observeGatewayFixture(t, f, created.ID)
	accountPath := root + "/" + created.ID + "/service_accounts"
	code, body = requestJSON(t, "POST", accountPath, owner, []byte(`{"name":"controller-credential","role":"openshell-admin"}`))
	var account struct {
		ID         string `json:"id"`
		ClientID   string `json:"client_id"`
		Credential struct {
			Secret string `json:"client_secret"`
		} `json:"credential"`
	}
	if code != 201 || json.Unmarshal(body, &account) != nil || account.Credential.Secret == "" {
		t.Fatalf("controller-bound account creation: %d", code)
	}
	response, grant := k.issue(t, account.ClientID, account.Credential.Secret)
	if response.StatusCode != 200 {
		t.Fatal("controller-bound credential cannot issue a token")
	}
	keys, err := k.http.Do(ctx, "GET", "/realms/workflow/protocol/openid-connect/certs", nil, nil)
	if err != nil || keys.StatusCode != 200 {
		t.Fatal("read Gateway verification keys")
	}
	expected, _ := keycloak.GatewayClientID(created.ID)
	identity, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: expected, RolesClaim: "hypershell.roles"}, grant["access_token"].(string), keys.Body)
	if err != nil || len(identity.Roles) != 2 {
		t.Fatalf("controller-bound token validation: %v", err)
	}
	foreign, _ := keycloak.GatewayClientID(secondID)
	if _, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: foreign}, grant["access_token"].(string), keys.Body); err == nil {
		t.Fatal("Gateway token crossed the audience boundary")
	}
	if code, _ := requestJSON(t, "DELETE", accountPath+"/"+account.ID, owner, nil); code != 202 && code != 204 {
		t.Fatalf("remove controller-bound account: %d", code)
	}
	// Keep the controller alive while the API is absent. A new connection must
	// repeat the state scan and repair a change that has no live watch delivery.
	stopAPI()
	if _, err := f.db.Exec("UPDATE gateways SET oidc='invalid' WHERE id=$1", created.ID); err != nil {
		t.Fatal(err)
	}
	stopAPI, address, _ = startBoth(t, apiBinary, f.dsn, config, append(settings, "STEGO_GRPC_ADDR="+grpcAddress)...)
	root = address + "/api/hypershell/v1/gateways"
	if oidc := waitOIDC(created.ID); oidc != firstOIDC {
		t.Fatal("API reconnect did not recover current Gateway state")
	}
	if strings.Count(logs(), "Gateway identity watch started") < 2 {
		t.Fatal("controller did not open a new watch after API restart")
	}
	// The identity controller is absent during these changes. Restart must recover from
	// database state and provider inventory, without retained process memory.
	stopController()
	if code, _ := requestJSON(t, "PATCH", root+"/"+created.ID, owner, []byte(`{"oidc":"invalid","name":"renamed-identity"}`)); code != 200 {
		t.Fatalf("offline identity change: %d", code)
	}
	if _, err := client.DeleteGateway(ownerContext, &pb.DeleteGatewayRequest{Id: secondID}); err != nil {
		t.Fatal(err)
	}
	state, err := states.GetGatewayIdentityState(controlContext, &control.GetGatewayIdentityStateRequest{Id: secondID})
	if err != nil || !state.GetDeleted() {
		t.Fatalf("missing explicit deletion state: %v", err)
	}
	stopAPI()
	stopAPI, address, grpcAddress = startBoth(t, apiBinary, f.dsn, config, settings...)
	defer stopAPI()
	root = address + "/api/hypershell/v1/gateways"
	stopController, logs = startIdentityController(t, controllerBinary, k, grpcAddress, tlsIdentity.config.CAFile, controllerToken)
	defer stopController()
	if oidc := waitOIDC(created.ID); oidc != firstOIDC {
		t.Fatal("rename or restart changed the Gateway audience")
	}
	deadline := time.Now().Add(15 * time.Second)
	for k.gatewayClient(t, secondID) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("offline Gateway deletion left its client\n%s", logs())
		}
		time.Sleep(100 * time.Millisecond)
	}
	_, cleanupConnection := grpcClient(t, grpcAddress, tlsIdentity)
	cleanupStates := control.NewGatewayIdentityServiceClient(cleanupConnection)
	awaitCleanup := func() int64 {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for {
			state, err := cleanupStates.GetGatewayIdentityState(controlContext, &control.GetGatewayIdentityStateRequest{Id: secondID})
			if err == nil && state.Deleted && state.Cleanup["identity"] {
				return state.ResourceVersion
			}
			if time.Now().After(deadline) {
				t.Fatalf("identity cleanup was not recorded: %v\n%s", err, logs())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	completedVersion := awaitCleanup()
	stopController()
	provider, err := keycloak.NewClient(k.options)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	if _, err := provider.EnsureGateway(context.Background(), secondID, "late-effect"); err != nil {
		t.Fatal(err)
	}
	if k.gatewayClient(t, secondID) == nil {
		t.Fatal("late identity effect was not created")
	}
	stopController, logs = startIdentityController(t, controllerBinary, k, grpcAddress, tlsIdentity.config.CAFile, controllerToken)
	defer stopController()
	deadline = time.Now().Add(15 * time.Second)
	for k.gatewayClient(t, secondID) != nil {
		if time.Now().After(deadline) {
			t.Fatalf("completed cleanup skipped a late identity effect\n%s", logs())
		}
		time.Sleep(100 * time.Millisecond)
	}
	if revision := awaitCleanup(); revision != completedVersion {
		t.Fatal("unchanged cleanup repeated its write", revision, completedVersion)
	}
	if live := k.gatewayClient(t, created.ID); live == nil || live["name"] != "renamed-identity" {
		t.Fatal("restart did not retain the live Gateway identity")
	}
}
