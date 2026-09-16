package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/segmentio/ksuid"
)

// An owned legacy audience must permit account creation. Stop the provider at
// POST so this check does not replace the real access and signed-token tests.
func TestAccountPolicyReachesCreationWithLegacyAudience(t *testing.T) {
	gatewayID, accountID := ksuid.New().String(), ksuid.New().String()
	var creates atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/realms/test/protocol/openid-connect/token":
			w.Write([]byte(`{"access_token":"admin-token","expires_in":300,"token_type":"Bearer"}`))
		case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients":
			if r.URL.Query().Get("clientId") == "legacy-audience" {
				w.Write([]byte(`[{"id":"gateway-provider-id","clientId":"legacy-audience"}]`))
			} else {
				w.Write([]byte(`[]`))
			}
		case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients/gateway-provider-id":
			json.NewEncoder(w).Encode(map[string]any{"id": "gateway-provider-id", "clientId": "legacy-audience", "attributes": map[string]string{gatewayAttribute: "true", gatewayIDAttribute: gatewayID}})
		case r.Method == "POST" && r.URL.Path == "/admin/realms/test/clients":
			creates.Add(1)
			w.WriteHeader(http.StatusServiceUnavailable)
		case r.Method == "GET":
			w.WriteHeader(http.StatusNotFound)
		default:
			t.Error("unexpected provider mutation")
			w.WriteHeader(http.StatusInternalServerError)
		}
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
	client, err := NewClient(Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca, AccountJournal: testAccountJournals(t)})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	_, err = client.ProvisionServiceAccount(context.Background(), ServiceAccountSpec{ClientID: "hs-sa-" + gatewayID + "-" + accountID, DisplayName: "legacy account", GatewayClientID: "legacy-audience", GatewayID: gatewayID, ServiceAccountID: accountID, CreatorUserID: ksuid.New().String(), Role: RoleAdmin, ExpectedIssuer: server.URL + "/realms/test"})
	if creates.Load() != 1 || err == nil {
		t.Fatalf("account did not reach the refused create request: creates=%d error=%v", creates.Load(), err)
	}
}
