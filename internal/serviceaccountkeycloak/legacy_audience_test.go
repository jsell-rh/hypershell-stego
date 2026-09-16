package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/segmentio/ksuid"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestServiceAccountKeepsOwnedLegacyAudience(t *testing.T) {
	id := ksuid.New().String()
	for _, item := range []struct {
		name                    string
		current, foreign, mixed bool
		wantError               bool
	}{
		{name: "legacy"},
		{name: "foreign", foreign: true, wantError: true},
		{name: "current custom name", current: true, wantError: true},
		{name: "partial migration", mixed: true, wantError: true},
	} {
		t.Run(item.name, func(t *testing.T) {
			attrs := map[string]string{gatewayAttribute: "true", gatewayIDAttribute: id}
			if item.foreign {
				attrs[gatewayIDAttribute] = ksuid.New().String()
			}
			if item.current {
				attrs = map[string]string{managedGatewayAttribute: "true", managedGatewayIDAttribute: id}
			}
			if item.mixed {
				attrs[managedGatewayIDAttribute] = id
			}
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/realms/test/protocol/openid-connect/token" {
					w.Write([]byte(`{"access_token":"admin-token","expires_in":300,"token_type":"Bearer"}`))
					return
				}
				if r.Method != "GET" || r.URL.Path != "/admin/realms/test/clients/provider-id" {
					t.Error("audience check reached an unexpected effect")
					w.WriteHeader(500)
					return
				}
				json.NewEncoder(w).Encode(map[string]any{"id": "provider-id", "clientId": "legacy-audience", "attributes": attrs})
			}))
			defer server.Close()
			dir := t.TempDir()
			ca, secret := filepath.Join(dir, "ca"), filepath.Join(dir, "secret")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(secret, []byte("test-secret"), 0600); err != nil {
				t.Fatal(err)
			}
			client, err := NewClient(Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			policy, err := client.serviceAccountRoles(context.Background(), ServiceAccountSpec{GatewayID: id, GatewayClientID: "legacy-audience", Role: RoleUser}, "provider-id")
			if item.wantError {
				if err == nil {
					t.Fatal("foreign or incomplete audience was accepted")
				}
				return
			}
			if err != nil {
				t.Fatal("owned legacy audience was rejected", err)
			}
			if len(policy.Clients) != 1 || policy.Clients[0].Client.ID != "provider-id" || policy.Clients[0].Client.ClientID != "legacy-audience" || len(policy.Clients[0].Names) != 1 || policy.Clients[0].Names[0] != RoleUser {
				t.Fatal("legacy audience or role changed")
			}
		})
	}
}
