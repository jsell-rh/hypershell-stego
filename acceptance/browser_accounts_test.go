package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/out/auth"
)

// Gateway readiness is an explicit fixture input. This test checks the account
// UI and real identity provider, not Gateway workload provisioning.
func checkRenderedServiceAccounts(t *testing.T, f *fixture, k *keycloakFixture, subject string, browser *renderedBrowser, owner *consoleBrowser, runtimeLogs func() string) {
	t.Helper()
	issuer := k.options.ServerURL + "/realms/workflow"
	oidc, err := json.Marshal(map[string]string{"issuer": issuer, "client_id": "gateway-audience", "audience": "gateway-audience"})
	if err != nil {
		t.Fatal(err)
	}
	request := f.request("account-console-fixture")
	raw := string(oidc)
	request.OIDC = &raw
	gateway, err := f.service.Create(context.Background(), gateways.Principal{Issuer: issuer, Subject: subject, Username: "console-alice", Roles: []string{"gateway:creator"}}, request)
	if err != nil {
		t.Fatal(err)
	}
	k.bindGateway(t, "gateway-audience", gateway.ID)
	observeGatewayFixture(t, f, gateway.ID)
	private := filepath.Join(t.TempDir(), "credential.json")
	input := map[string]any{"origin": browser.Origin, "pins": browser.Pins, "gateway": gateway.ID, "privateFile": private}
	state := struct {
		Session string `json:"session"`
		ID      string `json:"id"`
	}{}
	run := func(phase string) {
		t.Helper()
		input["session"], input["id"] = state.Session, state.ID
		path := filepath.Join(browser.directory, "accounts-input.json")
		output := filepath.Join(browser.directory, "accounts-"+phase+".json")
		data, _ := json.Marshal(input)
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "node", "browser_accounts_workflow.mjs", path, output, phase)
		cmd.Env = append(os.Environ(), "NODE_OPTIONS=--max-old-space-size=256")
		if logs, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("rendered account %s: %v\n%s", phase, err, logs)
		}
		if phase == "close" {
			return
		}
		data, err := os.ReadFile(output)
		if err != nil {
			t.Fatal(err)
		}
		if json.Unmarshal(data, &state) != nil || state.Session == "" || state.ID == "" {
			t.Fatal("account browser returned incomplete state")
		}
	}
	t.Cleanup(func() {
		if state.Session != "" {
			run("close")
		}
	})
	run("create")
	data, err := os.ReadFile(private)
	if err != nil {
		t.Fatal("one-time credential was not captured privately")
	}
	if err := os.Remove(private); err != nil {
		t.Fatal(err)
	}
	var credential struct {
		ID       string `json:"id"`
		ClientID string `json:"client_id"`
		Secret   string `json:"secret"`
	}
	if json.Unmarshal(data, &credential) != nil || credential.ID != state.ID || credential.ClientID == "" || len(credential.Secret) < 16 {
		t.Fatal("incomplete one-time credential")
	}
	clear(data)
	response, grant := k.issue(t, credential.ClientID, credential.Secret)
	if response.StatusCode != 200 {
		t.Fatal("rendered credential cannot obtain a token", response.StatusCode)
	}
	token, ok := grant["access_token"].(string)
	if !ok {
		t.Fatal("credential token missing")
	}
	keys, err := k.http.Do(context.Background(), "GET", "/realms/workflow/protocol/openid-connect/certs", nil, nil)
	if err != nil || keys.StatusCode != 200 {
		t.Fatal("provider keys unavailable")
	}
	identity, err := auth.VerifyWithJWKS(auth.Config{Issuer: issuer, Audience: "gateway-audience", RolesClaim: "hypershell.roles"}, token, keys.Body)
	if err != nil || identity.UserID == "" {
		t.Fatal("rendered credential token failed verification")
	}
	contains := func(values []string, want string) bool {
		for _, v := range values {
			if v == want {
				return true
			}
		}
		return false
	}
	if !contains(identity.Roles, "openshell-admin") {
		t.Fatal("account role was not applied")
	}
	var storedSubject string
	if err := f.db.QueryRow("SELECT subject FROM service_accounts WHERE id=$1", state.ID).Scan(&storedSubject); err != nil || storedSubject != identity.UserID {
		t.Fatal("credential token does not identify the stored account")
	}
	if _, err := auth.VerifyWithJWKS(auth.Config{Issuer: issuer, Audience: "another-gateway"}, token, keys.Body); err == nil {
		t.Fatal("credential token crossed the Gateway audience boundary")
	}
	if grant["refresh_token"] != nil {
		t.Fatal("service credential issued a refresh token")
	}
	for _, path := range []string{"/gateways/" + gateway.ID + "/service_accounts", "/gateways/" + gateway.ID + "/service_accounts/" + state.ID} {
		response := owner.api(t, "GET", path, nil)
		if response.StatusCode != 200 {
			t.Fatal("account read failed", response.StatusCode)
		}
		var body any
		if json.Unmarshal(response.Body, &body) != nil {
			t.Fatal("invalid account read response")
		}
		canonical, err := json.Marshal(body)
		if err != nil || bytes.Contains(canonical, []byte(credential.Secret)) || bytes.Contains(canonical, []byte(`"client_secret":`)) {
			t.Fatal("read response disclosed a credential")
		}
	}
	for _, table := range []string{"service_accounts", "service_account_audits", "stego_outbox.messages"} {
		var rows string
		if err := f.db.QueryRow("SELECT COALESCE(json_agg(t)::text,'[]') FROM " + table + " t").Scan(&rows); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(rows, credential.Secret) {
			t.Fatal("credential entered durable application data", table)
		}
	}
	run("revoke")
	response, _ = k.issue(t, credential.ClientID, credential.Secret)
	if response.StatusCode != 401 {
		t.Fatal("revoked rendered credential still issues tokens", response.StatusCode)
	}
	run("delete")
	if response := owner.api(t, "GET", "/gateways/"+gateway.ID+"/service_accounts/"+state.ID, nil); response.StatusCode != 404 {
		t.Fatal("deleted account is still visible", response.StatusCode)
	}
	if strings.Contains(runtimeLogs(), credential.Secret) {
		t.Fatal("credential entered runtime logs")
	}
	run("close")
	state.Session = ""
	t.Log("Rendered account creation, one-time handoff, verified token issuance, reload, revoke, and delete passed")
}
