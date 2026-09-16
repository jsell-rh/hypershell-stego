package acceptance

import (
	"context"
	"encoding/json"
	"errors"
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
	observeGatewayFixture(t, f, gateway.ID)
	key, auth := issuer(t)
	providerSettings, stopProvider := startRealProvisioner(t, f, k, key, auth)
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
		// Model an orphan from the old provider. Current ownership requires its
		// saved journal and cannot be adopted from a public name alone.
		path := "/clients/" + created.ClientUUID
		response := k.adminRequest(t, "GET", path, nil)
		var live map[string]any
		if json.Unmarshal(response.Body, &live) != nil {
			t.Fatal("invalid orphan fixture")
		}
		attrs, ok := live["attributes"].(map[string]any)
		if !ok {
			t.Fatal("orphan attributes are missing")
		}
		for _, name := range []string{"hypershell.service-account", "hypershell.gateway-id", "hypershell.service-account-id"} {
			attrs[name] = attrs["stego.owner."+name]
			delete(attrs, "stego.owner."+name)
		}
		// First reproduce the bad fixture: omitted current keys remain present.
		// The production inventory must reject this mixed ownership.
		k.adminRequest(t, "PUT", path, map[string]any{"attributes": attrs})
		if _, err := provider.ListManagedClients(context.Background(), gatewayID); !errors.Is(err, keycloak.ErrNotManaged) {
			t.Fatal("mixed orphan ownership was not rejected", err)
		}
		for _, name := range []string{"hypershell.service-account", "hypershell.gateway-id", "hypershell.service-account-id"} {
			attrs["stego.owner."+name] = ""
		}
		k.adminRequest(t, "PUT", path, map[string]any{"attributes": attrs})
		response = k.adminRequest(t, "GET", path, nil)
		var confirmed struct {
			Attributes map[string]string `json:"attributes"`
		}
		if json.Unmarshal(response.Body, &confirmed) != nil {
			t.Fatal("invalid saved orphan fixture")
		}
		for name, want := range map[string]string{"hypershell.service-account": "true", "hypershell.gateway-id": gatewayID, "hypershell.service-account-id": id} {
			_, current := confirmed.Attributes["stego.owner."+name]
			if current || confirmed.Attributes[name] != want {
				t.Fatal("orphan fixture retained mixed ownership")
			}
		}
		return credential{created.ClientID, created.ClientSecret}
	}
	credentials = append(credentials, provision(gateway.ID, "gateway-audience"))
	foreignID := ksuid.New().String()
	if _, err := provider.EnsureGateway(context.Background(), foreignID, "other-gateway", 1); err != nil {
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
	if code, _ := requestJSON(t, "DELETE", address+path, owner, nil); code != 202 {
		t.Fatal("provider outage deletion", code)
	}
	if code, _ := requestJSON(t, "GET", address+path, owner, nil); code != 200 {
		t.Fatal("outage removed Gateway", code)
	}
	stopAPI()
	providerSettings, stopProvider = startRealProvisioner(t, f, k, key, auth)
	stopAPI, address = startApplication(t, binary, f.dsn, config, append(auth, providerSettings...)...)
	started := time.Now()
	// Only the account controller runs in this fixture. Wait for its durable
	// completion, including orphan inventory, without forging other owners.
	deadline := time.Now().Add(30 * time.Second)
	for {
		var complete bool
		if err := f.db.QueryRow("SELECT COALESCE(stego_cleanup->>'accounts'='true',false) FROM gateways WHERE id=$1", gateway.ID).Scan(&complete); err != nil {
			t.Fatal(err)
		}
		if complete {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("restart did not complete real account cleanup")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if code, _ := requestJSON(t, "GET", address+path, owner, nil); code != 200 {
		t.Fatal("account controller bypassed other cleanup owners", code)
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
