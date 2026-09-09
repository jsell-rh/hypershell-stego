package acceptance

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/hex"
	"encoding/json"
	"fmt"
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
	web "github.com/jsell-rh/hypershell-stego/out/application/client"
	"github.com/jsell-rh/hypershell-stego/out/auth"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const keycloakImage = "quay.io/keycloak/keycloak@sha256:ff4257d0d64efbe99ed1ddfaf07765cc3c36dc7518bf8324d41961327f441c54"

type keycloakFixture struct {
	options keycloak.Options
	http    *web.Client
}

func startKeycloak(t *testing.T) *keycloakFixture {
	t.Helper()
	if os.Getenv("STEGO_REQUIRE_KEYCLOAK") != "1" {
		t.Skip("set STEGO_REQUIRE_KEYCLOAK=1 for the real Keycloak workflow")
	}
	identity := identity(t, "localhost")
	certDir := filepath.Dir(identity.config.CAFile)
	realmDir := t.TempDir()
	realm := map[string]any{
		"realm": "workflow", "enabled": true, "sslRequired": "all",
		"clients": []any{
			map[string]any{"clientId": "provisioner", "enabled": true, "secret": "acceptance-only-admin-secret", "serviceAccountsEnabled": true, "standardFlowEnabled": false, "directAccessGrantsEnabled": false, "fullScopeAllowed": true},
			map[string]any{"clientId": "gateway-audience", "enabled": true, "publicClient": false, "standardFlowEnabled": false},
		},
		"roles": map[string]any{"client": map[string]any{"gateway-audience": []any{map[string]any{"name": "openshell-user"}, map[string]any{"name": "openshell-admin"}}}},
		"users": []any{map[string]any{"username": "service-account-provisioner", "enabled": true, "serviceAccountClientId": "provisioner", "clientRoles": map[string]any{"realm-management": []string{"manage-clients", "view-clients", "manage-users", "view-users"}}}},
	}
	data, err := json.Marshal(realm)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(realmDir, "workflow-realm.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	name := "stego-keycloak-" + hex.EncodeToString(makeRandom(t, 8))
	export := t.TempDir()
	for _, file := range []string{"server.pem", "server-key.pem"} {
		content, err := os.ReadFile(filepath.Join(certDir, file))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(export, file), content, 0444); err != nil {
			t.Fatal(err)
		}
	}
	realmFile := filepath.Join(realmDir, "workflow-realm.json")
	if err := os.Chmod(realmFile, 0444); err != nil {
		t.Fatal(err)
	}
	args := []string{"run", "--detach", "--name", name, "--memory=2g", "--cpus=2", "--publish", "127.0.0.1::8443",
		"--mount", "type=bind,source=" + filepath.Join(export, "server.pem") + ",target=/certs/server.pem,readonly",
		"--mount", "type=bind,source=" + filepath.Join(export, "server-key.pem") + ",target=/certs/server-key.pem,readonly",
		"--mount", "type=bind,source=" + realmFile + ",target=/opt/keycloak/data/import/workflow-realm.json,readonly",
		keycloakImage, "start-dev", "--http-enabled=false", "--hostname-strict=false", "--https-certificate-file=/certs/server.pem", "--https-certificate-key-file=/certs/server-key.pem", "--https-protocols=TLSv1.3", "--import-realm"}

	command := exec.Command("docker", args...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("start Keycloak: %v %s", err, output)
	}
	t.Cleanup(func() {
		if output, err := exec.Command("docker", "rm", "--force", name).CombinedOutput(); err != nil {
			t.Errorf("remove Keycloak: %v %s", err, output)
		}
	})
	output, err := exec.Command("docker", "port", name, "8443/tcp").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	base := "https://" + strings.TrimSpace(string(output))
	client, err := web.New(web.Options{BaseURL: base, CAFile: identity.config.CAFile})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	deadline := time.Now().Add(2 * time.Minute)
	for {
		response, err := client.Do(context.Background(), http.MethodGet, "/realms/workflow/.well-known/openid-configuration", nil, nil)
		if err == nil && response.StatusCode == 200 {
			break
		}
		running, _ := exec.Command("docker", "inspect", "--format", "{{.State.Running}}", name).CombinedOutput()
		if time.Now().After(deadline) || strings.TrimSpace(string(running)) != "true" {
			logs, _ := exec.Command("docker", "logs", name).CombinedOutput()
			t.Fatalf("Keycloak did not start: %s", logs)
		}
		time.Sleep(500 * time.Millisecond)
	}
	secretFile := filepath.Join(t.TempDir(), "admin-secret")
	if err := os.WriteFile(secretFile, []byte("acceptance-only-admin-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	return &keycloakFixture{options: keycloak.Options{ServerURL: base, Realm: "workflow", ClientID: "provisioner", SecretFile: secretFile, CAFile: identity.config.CAFile}, http: client}
}
func makeRandom(t *testing.T, size int) []byte {
	t.Helper()
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}
func (k *keycloakFixture) issue(t *testing.T, id, secret string) (web.Response, map[string]any) {
	t.Helper()
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {id}, "client_secret": {secret}}
	response, err := k.http.Do(context.Background(), "POST", "/realms/workflow/protocol/openid-connect/token", http.Header{"Content-Type": []string{"application/x-www-form-urlencoded"}}, []byte(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	var result map[string]any
	if json.Unmarshal(response.Body, &result) != nil {
		t.Fatal("invalid token response")
	}
	return response, result
}
func startRealProvisioner(t *testing.T, k *keycloakFixture, key *rsa.PrivateKey, settings []string) ([]string, func()) {
	t.Helper()
	binary := buildProgram(t, "./cmd/provisioner")
	identity := identity(t, "localhost")
	dir := filepath.Dir(identity.config.CAFile)
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary)
	command.Env = append(os.Environ(), settings...)
	command.Env = append(command.Env, "STEGO_GRPC_ADDR=127.0.0.1:0", "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"), "HYPERSHELL_KEYCLOAK_URL="+k.options.ServerURL, "HYPERSHELL_KEYCLOAK_REALM="+k.options.Realm, "HYPERSHELL_KEYCLOAK_CLIENT_ID="+k.options.ClientID, "HYPERSHELL_KEYCLOAK_SECRET_FILE="+k.options.SecretFile, "HYPERSHELL_KEYCLOAK_CA_FILE="+k.options.CAFile, `HYPERSHELL_PROVISIONER_SUBJECTS=["api-provisioner"]`)
	if raceEnabled {
		command.Env = append(command.Env, "GORACE=halt_on_error=1 exitcode=66")
	}
	output := runtimeOutput{ready: make(chan string, 1), grpcReady: make(chan string, 1)}
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		cancel()
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
		defer cancel()
		command.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("provisioner exit: %v %s", err, output.String())
			}
		case <-time.After(12 * time.Second):
			cancel()
			<-done
			t.Error("provisioner shutdown timed out")
		}
	}
	t.Cleanup(stop)
	var address string
	select {
	case address = <-output.grpcReady:
	case err := <-done:
		stopped = true
		cancel()
		t.Fatalf("provisioner startup: %v %s", err, output.String())
	case <-time.After(10 * time.Second):
		t.Fatal("provisioner startup timed out")
	}
	tokenFile := filepath.Join(t.TempDir(), "service-token")
	if err := os.WriteFile(tokenFile, []byte(token(t, key, "api-provisioner")), 0600); err != nil {
		t.Fatal(err)
	}
	return []string{"HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_ADDR=" + address, "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_CA_FILE=" + identity.config.CAFile, "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_TOKEN_FILE=" + tokenFile}, stop
}

func TestServiceAccountsWithRealKeycloak(t *testing.T) {
	k := startKeycloak(t)
	f := database(t)
	_, gateway := accountService(t, f, newAccountProvider())
	oidc := fmt.Sprintf(`{"issuer":%q,"client_id":"gateway-audience","audience":"gateway-audience"}`, k.options.ServerURL+"/realms/workflow")
	if _, err := f.db.Exec("UPDATE gateways SET oidc=$1 WHERE id=$2", oidc, gateway.ID); err != nil {
		t.Fatal(err)
	}
	key, settings := issuer(t)
	providerSettings, stopProvider := startRealProvisioner(t, k, key, settings)
	settings = append(settings, providerSettings...)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stopAPI, address := startApplication(t, binary, f.dsn, config, settings...)
	path := "/api/hypershell/v1/gateways/" + gateway.ID + "/service_accounts"
	owner := token(t, key, "alice")
	code, data := requestJSON(t, "POST", address+path, owner, []byte(`{"name":"real-keycloak","role":"openshell-admin"}`))
	if code != 201 {
		t.Fatalf("account creation failed: status %d, %s", code, data)
	}
	var created struct {
		ID         string `json:"id"`
		ClientID   string `json:"client_id"`
		Credential struct {
			Secret string `json:"client_secret"`
		} `json:"credential"`
	}
	if json.Unmarshal(data, &created) != nil || created.ID == "" || created.Credential.Secret == "" {
		t.Fatal("incomplete creation response")
	}
	rowAny, err := f.storage.Get(context.Background(), "ServiceAccount", created.ID)
	if err != nil {
		t.Fatal(err)
	}
	row := rowAny.(model.ServiceAccount)
	response, grant := k.issue(t, row.ClientID, created.Credential.Secret)
	if response.StatusCode != 200 {
		t.Fatal("issued credential cannot obtain a token")
	}
	if grant["refresh_token"] != nil {
		t.Fatal("client credential issued a refresh token")
	}
	keys, err := k.http.Do(context.Background(), "GET", "/realms/workflow/protocol/openid-connect/certs", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	id, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: "gateway-audience", RolesClaim: "hypershell.roles"}, grant["access_token"].(string), keys.Body)
	if err != nil || id.UserID != row.Subject || len(id.Roles) != 2 {
		t.Fatalf("issued token validation: %v", err)
	}
	if _, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: "another-gateway"}, grant["access_token"].(string), keys.Body); err == nil {
		t.Fatal("token granted cross-Gateway access")
	}
	for _, suffix := range []string{"", "/" + created.ID} {
		code, body := requestJSON(t, "GET", address+path+suffix, owner, nil)
		if code != 200 || strings.Contains(string(body), created.Credential.Secret) {
			t.Fatal("read failed or exposed secret")
		}
	}
	// The generated RPC runtime rejects a verified but unlisted caller.
	rpcOptions := rpc.Options{}
	for _, setting := range providerSettings {
		name, value, _ := strings.Cut(setting, "=")
		switch name {
		case "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_ADDR":
			rpcOptions.Address = value
		case "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_CA_FILE":
			rpcOptions.CAFile = value
		case "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_TOKEN_FILE":
			rpcOptions.TokenFile = value
		}
	}
	if err := os.WriteFile(rpcOptions.TokenFile, []byte(token(t, key, "another-service")), 0600); err != nil {
		t.Fatal(err)
	}
	connection, err := rpc.New(rpcOptions)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	_, err = pb.NewOpenShellGatewayServiceAccountProvisionerServiceClient(connection).ListManaged(context.Background(), &pb.ListManagedRequest{GatewayId: gateway.ID})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatalf("unlisted caller: %v", err)
	}
	os.WriteFile(rpcOptions.TokenFile, []byte(token(t, key, "api-provisioner")), 0600)
	// Repair real provider drift before the terminal operation.
	_, adminGrant := k.issue(t, "provisioner", "acceptance-only-admin-secret")
	adminHeaders := http.Header{"Authorization": []string{"Bearer " + adminGrant["access_token"].(string)}, "Content-Type": []string{"application/json"}}
	clientPath := "/admin/realms/workflow/clients/" + row.ClientUuid
	current, err := k.http.Do(context.Background(), "GET", clientPath, adminHeaders, nil)
	if err != nil || current.StatusCode != 200 {
		t.Fatal("cannot read provisioned client")
	}
	var representation map[string]any
	if json.Unmarshal(current.Body, &representation) != nil {
		t.Fatal("invalid client representation")
	}
	representation["fullScopeAllowed"] = true
	representation["standardFlowEnabled"] = true
	payload, _ := json.Marshal(representation)
	changed, err := k.http.Do(context.Background(), "PUT", clientPath, adminHeaders, payload)
	if err != nil || changed.StatusCode != 204 {
		t.Fatal("cannot inject provider drift")
	}
	actualProvider, err := keycloak.NewClient(k.options)
	if err != nil {
		t.Fatal(err)
	}
	defer actualProvider.Close()
	spec := keycloak.ServiceAccountSpec{ClientID: row.ClientID, DisplayName: row.Name, GatewayClientID: "gateway-audience", GatewayID: gateway.ID, ServiceAccountID: row.ID, CreatorUserID: row.CreatedByUserID, Role: row.Role, ExpectedIssuer: k.options.ServerURL + "/realms/workflow", AccessTokenLifetimeSeconds: 300}
	if err := actualProvider.ReconcileServiceAccount(context.Background(), spec, row.ClientUuid, row.Subject, true); err != nil {
		t.Fatalf("repair real provider drift: %v", err)
	}
	repaired, err := k.http.Do(context.Background(), "GET", clientPath, adminHeaders, nil)
	if err != nil {
		t.Fatal(err)
	}
	if json.Unmarshal(repaired.Body, &representation) != nil || representation["fullScopeAllowed"] != false || representation["standardFlowEnabled"] != false {
		t.Fatal("provider drift survived repair")
	}
	// An actual creator downgrade must change future provider tokens.
	if _, err := f.db.Exec("UPDATE role_bindings SET role_id=(SELECT id FROM roles WHERE name='gateway:viewer') WHERE gateway_id=$1 AND user_id=$2", gateway.ID, row.CreatedByUserID); err != nil {
		t.Fatal(err)
	}
	roleDeadline := time.Now().Add(20 * time.Second)
	for {
		response, grant := k.issue(t, row.ClientID, created.Credential.Secret)
		if response.StatusCode == 200 {
			identity, err := auth.VerifyWithJWKS(auth.Config{Issuer: spec.ExpectedIssuer, Audience: spec.GatewayClientID, RolesClaim: "hypershell.roles"}, grant["access_token"].(string), keys.Body)
			if err == nil && len(identity.Roles) == 1 && identity.Roles[0] == "openshell-user" {
				break
			}
		}
		if time.Now().After(roleDeadline) {
			t.Fatal("creator downgrade did not reduce actual token roles")
		}
		time.Sleep(200 * time.Millisecond)
	}

	stopProvider()
	code, _ = requestJSON(t, "POST", address+path+"/"+created.ID+"/revoke", owner, []byte(`{}`))
	if code != 202 {
		t.Fatalf("offline revoke: %d", code)
	}
	stopAPI()
	providerSettings, stopProvider = startRealProvisioner(t, k, key, settings[:len(settings)-3])
	defer stopProvider()
	settings = append(settings[:len(settings)-3], providerSettings...)
	stopAPI, address = startApplication(t, binary, f.dsn, config, settings...)
	defer stopAPI()
	deadline := time.Now().Add(20 * time.Second)
	for {
		response, _ := k.issue(t, row.ClientID, created.Credential.Secret)
		if response.StatusCode == 401 || response.StatusCode == 400 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("revocation did not stop token issuance after restart")
		}
		time.Sleep(200 * time.Millisecond)
	}
	code, _ = requestJSON(t, "DELETE", address+path+"/"+created.ID, owner, nil)
	if code != 204 {
		t.Fatalf("account delete: %d", code)
	}
	response, _ = k.issue(t, row.ClientID, created.Credential.Secret)
	if response.StatusCode == 200 {
		t.Fatal("deleted client can issue tokens")
	}
	var persisted string
	if err := f.db.QueryRow("SELECT row_to_json(s)::text FROM service_accounts s WHERE id=$1", created.ID).Scan(&persisted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(persisted, created.Credential.Secret) {
		t.Fatal("secret reached durable account state")
	}
}
