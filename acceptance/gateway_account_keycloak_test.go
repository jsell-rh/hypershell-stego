package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	"github.com/segmentio/ksuid"
)

func TestGatewayDeletionWithProviderFailureAndOrphans(t *testing.T) {
	k := startKeycloak(t)
	f := database(t)
	_, gateway := accountService(t, f, newAccountProvider())
	k.bindGateway(t, "gateway-audience", gateway.ID)
	oidc := fmt.Sprintf(`{"issuer":%q,"client_id":"gateway-audience","audience":"gateway-audience"}`, k.options.ServerURL+"/realms/workflow")
	if _, err := f.db.Exec("UPDATE gateways SET oidc=$1 WHERE id=$2", oidc, gateway.ID); err != nil {
		t.Fatal(err)
	}
	key, auth := issuer(t)
	providerSettings, stopProvider := startRealProvisioner(t, k, key, auth)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stopAPI, address := startApplication(t, binary, f.dsn, config, append(auth, providerSettings...)...)
	defer func() { stopAPI(); stopProvider() }()
	owner := token(t, key, "alice")
	path := "/api/hypershell/v1/gateways/" + gateway.ID
	type credential struct {
		ClientID string `json:"client_id"`
		Secret   string `json:"client_secret"`
	}
	credentials := []credential{}
	for _, name := range []string{"first", "second"} {
		input, _ := json.Marshal(map[string]string{"name": name})
		code, data := requestJSON(t, "POST", address+path+"/service_accounts", owner, input)
		if code != 201 {
			t.Fatal("create real provider account", code, string(data))
		}
		var result struct {
			Credential credential `json:"credential"`
		}
		if json.Unmarshal(data, &result) != nil || result.Credential.Secret == "" {
			t.Fatal("missing credential")
		}
		credentials = append(credentials, result.Credential)
	}
	provider, err := keycloak.NewClient(k.options)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	provision := func(gatewayID, audience string) credential {
		t.Helper()
		id := ksuid.New().String()
		created, err := provider.ProvisionServiceAccount(context.Background(), keycloak.ServiceAccountSpec{ClientID: "hs-sa-" + gatewayID + "-" + id, DisplayName: "provider-only", GatewayClientID: audience, GatewayID: gatewayID, ServiceAccountID: id, CreatorUserID: ksuid.New().String(), Role: keycloak.RoleUser, ExpectedIssuer: k.options.ServerURL + "/realms/workflow", AccessTokenLifetimeSeconds: 300})
		if err != nil {
			t.Fatal("prepare provider-only account", err)
		}
		return credential{created.ClientID, created.ClientSecret}
	}
	credentials = append(credentials, provision(gateway.ID, "gateway-audience"))
	foreignID := ksuid.New().String()
	if _, err := provider.EnsureGateway(context.Background(), foreignID, "other-gateway"); err != nil {
		t.Fatal(err)
	}
	foreignAudience, err := keycloak.GatewayClientID(foreignID)
	if err != nil {
		t.Fatal(err)
	}
	foreign := provision(foreignID, foreignAudience)
	verify := func(c credential, want int) {
		t.Helper()
		response, _ := k.issue(t, c.ClientID, c.Secret)
		if response.StatusCode != want {
			t.Fatal("provider credential status", response.StatusCode, want)
		}
	}
	for _, c := range credentials {
		verify(c, 200)
	}
	verify(foreign, 200)
	stopProvider()
	if code, _ := requestJSON(t, "DELETE", address+path, owner, nil); code != 503 {
		t.Fatal("provider outage deletion", code)
	}
	if code, _ := requestJSON(t, "GET", address+path, owner, nil); code != 200 {
		t.Fatal("outage removed Gateway", code)
	}
	stopAPI()
	providerSettings, stopProvider = startRealProvisioner(t, k, key, auth)
	stopAPI, address = startApplication(t, binary, f.dsn, config, append(auth, providerSettings...)...)
	started := time.Now()
	if code, data := requestJSON(t, "DELETE", address+path, owner, nil); code != 204 {
		t.Fatal("restart cleanup", code, string(data))
	}
	t.Logf("Real provider cleanup completed in %s", time.Since(started))
	for _, c := range credentials {
		verify(c, 401)
	}
	verify(foreign, 200)
	clients, err := provider.ListManagedClients(context.Background(), gateway.ID)
	if err != nil || len(clients) != 0 {
		t.Fatal("Gateway cleanup left managed clients", len(clients), err)
	}
	var audits int
	if err := f.db.QueryRow("SELECT count(*) FROM service_account_audits WHERE gateway_id=$1 AND action='gateway_cleanup'", gateway.ID).Scan(&audits); err != nil || audits != 2 {
		t.Fatal("cleanup audit", audits, err)
	}
	t.Log("Restart removed two stored clients and one orphan; the other Gateway credential still works")
}
