package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/contracts"
	"github.com/jsell-rh/hypershell-stego/out/auth"
)

func TestGeneratedServiceAccountCLIWorkflow(t *testing.T) {
	k := startKeycloak(t)
	f := database(t)
	_, gateway := accountService(t, f, newAccountProvider())
	grantViewer(t, f, gateway.ID, "bob")
	k.bindGateway(t, "gateway-audience", gateway.ID)
	oidc := fmt.Sprintf(`{"issuer":%q,"client_id":"gateway-audience","audience":"gateway-audience"}`, k.options.ServerURL+"/realms/workflow")
	if _, err := f.db.Exec("UPDATE gateways SET oidc=$1 WHERE id=$2", oidc, gateway.ID); err != nil {
		t.Fatal(err)
	}
	observeGatewayFixture(t, f, gateway.ID)
	key, issuerSettings := issuer(t)
	providerSettings, stopProvider := startRealProvisioner(t, k, key, issuerSettings)
	defer func() { stopProvider() }()
	_, brokerConfig := broker(t, identity(t, "localhost"))
	apiBinary := buildApplication(t)
	settings := append(append([]string{}, issuerSettings...), providerSettings...)
	stopAPI, address := startApplication(t, apiBinary, f.dsn, brokerConfig, settings...)
	defer func() { stopAPI() }()
	var backend atomic.Value
	backend.Store(address)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target, err := url.Parse(backend.Load().(string))
		if err != nil {
			t.Error(err)
			return
		}
		httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
	}))
	proxy.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	proxy.StartTLS()
	defer proxy.Close()
	directory := t.TempDir()
	ca := filepath.Join(directory, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	config := filepath.Join(directory, "config.json")
	tokenFile := filepath.Join(directory, "token")
	owner := token(t, key, "alice")
	setToken := func(value string) {
		t.Helper()
		if err := os.WriteFile(tokenFile, []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	setToken(owner)
	cli := buildProgram(t, "./out/cli/cmd")
	run := func(args ...string) ([]byte, string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cli, args...)
		cmd.Env = append(os.Environ(), "HYPERSHELL_CONFIG="+config, "GORACE=atexit_sleep_ms=0")
		var out, problem bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &problem
		err := cmd.Run()
		if bytes.Contains(out.Bytes(), []byte(owner)) || strings.Contains(problem.String(), owner) || strings.Contains(problem.String(), "DATA RACE") {
			t.Fatal("CLI exposed a token or had a data race")
		}
		return out.Bytes(), problem.String(), err
	}
	success := func(args ...string) []byte {
		t.Helper()
		data, problem, err := run(args...)
		if err != nil {
			t.Fatalf("CLI command failed: %v %s", err, problem)
		}
		return data
	}
	success("login", "--url", proxy.URL, "--token-file", tokenFile, "--ca-file", ca)
	outputFile := filepath.Join(directory, "account.json")
	args := []string{"create", "service-account", "--gateway-id", gateway.ID, "--name", "cli-account", "--role", "openshell-admin"}
	count := func() int {
		t.Helper()
		var count int
		if err := f.db.QueryRow("SELECT count(*) FROM service_accounts").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	if data, _, err := run(args...); err == nil || len(data) != 0 || count() != 0 {
		t.Fatal("credential request did not require an output choice")
	}
	if err := os.WriteFile(outputFile, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if data, _, err := run(append(args, "--output-file", outputFile)...); err == nil || len(data) != 0 || count() != 0 {
		t.Fatal("existing output caused credential creation")
	}
	if err := os.Remove(outputFile); err != nil {
		t.Fatal(err)
	}
	if data := success(append(args, "--output-file", outputFile)...); len(data) != 0 {
		t.Fatal("credential command wrote stdout")
	}
	data, err := os.ReadFile(outputFile)
	if err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(outputFile)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal("credential file is not private")
	}
	var created struct {
		ID, ClientID string
		Credential   struct {
			Secret string `json:"client_secret"`
		}
	}
	var object map[string]any
	if json.Unmarshal(data, &object) != nil {
		t.Fatal("invalid credential JSON")
	}
	created.ID, _ = object["id"].(string)
	created.ClientID, _ = object["client_id"].(string)
	credential, ok := object["credential"].(map[string]any)
	if !ok {
		t.Fatal("missing credential")
	}
	created.Credential.Secret, _ = credential["client_secret"].(string)
	if created.ID == "" || created.Credential.Secret == "" {
		t.Fatal("incomplete account creation")
	}
	reference, err := contracts.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	route := reference.OpenAPI.Paths.Value("/api/hypershell/v1/gateways/{gateway_id}/service_accounts")
	if err := route.Post.Responses.Value("201").Value.Content["application/json"].Schema.Value.VisitJSON(object); err != nil {
		t.Fatal(err)
	}
	response, grant := k.issue(t, created.ClientID, created.Credential.Secret)
	if response.StatusCode != 200 {
		t.Fatal("CLI credential cannot obtain a token")
	}
	keys, err := k.http.Do(context.Background(), "GET", "/realms/workflow/protocol/openid-connect/certs", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	identity, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: "gateway-audience", RolesClaim: "hypershell.roles"}, grant["access_token"].(string), keys.Body)
	if err != nil || len(identity.Roles) != 2 {
		t.Fatal("CLI credential has incorrect roles")
	}
	checkPublic := func(data []byte) {
		t.Helper()
		if bytes.Contains(data, []byte(created.Credential.Secret)) || bytes.Contains(data, []byte(`"client_secret":`)) {
			t.Fatal("later account response exposed a secret")
		}
	}
	checkPublic(success("get", "serviceAccount", created.ID, "--gateway-id", gateway.ID))
	data = success("list", "serviceAccounts", "--gateway-id", gateway.ID, "--status", "ready", "--search", "cli-account", "--sort", "name", "--order", "asc")
	checkPublic(data)
	var ownerList struct {
		Total int
		Items []struct{ ID string }
	}
	if json.Unmarshal(data, &ownerList) != nil || ownerList.Total != 1 || len(ownerList.Items) != 1 || ownerList.Items[0].ID != created.ID {
		t.Fatal("owner filter did not return the created account")
	}
	var stored string
	if err := f.db.QueryRow("SELECT row_to_json(service_accounts)::text FROM service_accounts WHERE id=$1", created.ID).Scan(&stored); err != nil || strings.Contains(stored, created.Credential.Secret) {
		t.Fatal("account storage contains a secret")
	}
	_, otherGateway := accountService(t, f, newAccountProvider())
	if data, problem, err := run("get", "service-account", created.ID, "--gateway-id", otherGateway.ID); err == nil || len(data) != 0 || !strings.Contains(problem, "HTTP 404") {
		t.Fatal("account escaped its Gateway scope")
	}
	setToken(token(t, key, "bob"))
	if data, problem, err := run("get", "service-account", created.ID, "--gateway-id", gateway.ID); err == nil || len(data) != 0 || !strings.Contains(problem, "HTTP 404") {
		t.Fatal("viewer read another creator's account")
	}
	var list struct {
		Total int
		Items []any
	}
	data = success("list", "serviceAccount", "--gateway-id", gateway.ID)
	if json.Unmarshal(data, &list) != nil || list.Total != 0 || len(list.Items) != 0 {
		t.Fatal("viewer list exposed another creator's account")
	}
	deniedOutput := filepath.Join(directory, "denied.json")
	if data, problem, err := run("create", "service-account", "--gateway-id", gateway.ID, "--name", "forbidden", "--role", "openshell-admin", "--output-file", deniedOutput); err == nil || len(data) != 0 || !strings.Contains(problem, "HTTP 403") {
		t.Fatal("viewer obtained an admin credential")
	}
	if _, err := os.Stat(deniedOutput); !os.IsNotExist(err) || count() != 1 {
		t.Fatal("denied request left output or an account")
	}
	setToken(owner)
	stopAPI()
	stopProvider()
	providerSettings, stopProvider = startRealProvisioner(t, k, key, issuerSettings)
	settings = append(append([]string{}, issuerSettings...), providerSettings...)
	stopAPI, address = startApplication(t, apiBinary, f.dsn, brokerConfig, settings...)
	backend.Store(address)
	checkPublic(success("get", "service-account", created.ID, "--gateway-id", gateway.ID))
	checkPublic(success("revoke", "service-account", created.ID, "--gateway-id", gateway.ID))
	response, _ = k.issue(t, created.ClientID, created.Credential.Secret)
	if response.StatusCode != 401 {
		t.Fatal("revoked CLI credential can still obtain tokens")
	}
	data = success("list", "service-accounts", "--gateway-id", gateway.ID, "--status", "revoked")
	if json.Unmarshal(data, &list) != nil || list.Total != 1 {
		t.Fatal("revoked account was not retained in history")
	}
	checkPublic(data)
	if data, _, err := run("delete", "service-account", created.ID, "--gateway-id", gateway.ID); err == nil || len(data) != 0 {
		t.Fatal("delete did not require confirmation")
	}
	success("delete", "service-account", created.ID, "--gateway-id", gateway.ID, "--yes")
	if data, problem, err := run("get", "service-account", created.ID, "--gateway-id", gateway.ID); err == nil || len(data) != 0 || !strings.Contains(problem, "HTTP 404") {
		t.Fatal("deleted account remains visible")
	}
	success("logout")
	if data, err := os.ReadFile(outputFile); err != nil || !bytes.Contains(data, []byte(created.Credential.Secret)) {
		t.Fatal("logout removed the caller's credential file")
	}
	t.Log("The generated CLI completed protected credential output, real token issuance, filtered access, restart, revocation, and deletion")
}
