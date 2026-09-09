package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/segmentio/ksuid"
)

func TestGatewayBindingBeforeRoleLookupOrCreation(t *testing.T) {
	gatewayID := ksuid.New().String()
	accountID := ksuid.New().String()
	for _, test := range []struct {
		name, clientID string
		attributes     map[string]string
	}{
		{"missing", "audience", nil},
		{"missing marker", "audience", map[string]string{"hypershell.gateway-id": gatewayID}},
		{"missing ID", "audience", map[string]string{"hypershell.gateway": "true"}},
		{"foreign ID", "audience", map[string]string{"hypershell.gateway": "true", "hypershell.gateway-id": ksuid.New().String()}},
		{"wrong client", "different", map[string]string{"hypershell.gateway": "true", "hypershell.gateway-id": gatewayID}},
	} {
		t.Run(test.name, func(t *testing.T) {
			var unauthorized atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/realms/test/protocol/openid-connect/token":
					w.Write([]byte(`{"access_token":"test-admin-token","expires_in":300}`))
				case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients":
					w.Write([]byte(`[{"id":"gateway-uuid","clientId":"audience"}]`))
				case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients/gateway-uuid":
					json.NewEncoder(w).Encode(map[string]any{"id": "gateway-uuid", "clientId": test.clientID, "attributes": test.attributes})
				case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients/account-uuid":
					json.NewEncoder(w).Encode(map[string]any{"id": "account-uuid", "clientId": "hs-sa-" + gatewayID + "-" + accountID, "attributes": map[string]string{managedAttribute: "true", gatewayIDAttribute: gatewayID, serviceAccountIDAttribute: accountID}})
				default:
					unauthorized.Add(1)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			ca, secret := filepath.Join(t.TempDir(), "ca"), filepath.Join(t.TempDir(), "secret")
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
			spec := ServiceAccountSpec{ClientID: "hs-sa-" + gatewayID + "-" + accountID, GatewayClientID: "audience", GatewayID: gatewayID, ServiceAccountID: accountID, CreatorUserID: ksuid.New().String(), ExpectedIssuer: server.URL + "/realms/test", Role: RoleAdmin}
			if _, err := client.ProvisionServiceAccount(context.Background(), spec); err == nil {
				t.Error("accepted invalid Gateway binding")
			}
			if err := client.ReconcileServiceAccount(context.Background(), spec, "account-uuid", "subject", true); err == nil {
				t.Error("reconciliation accepted invalid Gateway binding")
			}
			if unauthorized.Load() != 0 {
				t.Fatal("invalid Gateway binding reached role lookup or client creation")
			}
		})
	}
}

func TestOwnershipChecksBeforeMutation(t *testing.T) {
	var writes atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/realms/test/protocol/openid-connect/token" {
			w.Write([]byte(`{"access_token":"test-admin-token","expires_in":300}`))
			return
		}
		if r.Method != "GET" {
			writes.Add(1)
			t.Error("invalid ownership reached mutation")
		}
		json.NewEncoder(w).Encode(map[string]any{"id": "uuid", "clientId": "unrelated", "attributes": map[string]string{managedAttribute: "true", gatewayIDAttribute: "gateway", serviceAccountIDAttribute: "account"}})
	}))
	defer server.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca")
	secret := filepath.Join(dir, "secret")
	os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600)
	os.WriteFile(secret, []byte("test-secret"), 0600)
	c, err := NewClient(Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	for _, ids := range [][2]string{{"", "account"}, {"gateway", ""}, {"other", "account"}, {"gateway", "other"}, {"gateway", "account"}} {
		for _, remove := range []bool{false, true} {
			var err error
			if remove {
				err = c.DeleteServiceAccount(context.Background(), "uuid", ids[0], ids[1])
			} else {
				err = c.DisableServiceAccount(context.Background(), "uuid", ids[0], ids[1])
			}
			if !errors.Is(err, ErrNotManaged) {
				t.Fatalf("ownership mismatch: %v", err)
			}
		}
	}
	if writes.Load() != 0 {
		t.Fatal("ownership check allowed a write")
	}
	for _, spec := range []ServiceAccountSpec{{}, {ClientID: "x", GatewayID: "gateway", ServiceAccountID: "account", CreatorUserID: "user", ExpectedIssuer: server.URL + "/realms/test", Role: RoleUser}, {ClientID: "hs-sa-gateway-account", ExpectedIssuer: "https://untrusted.example"}} {
		if _, err := c.ProvisionServiceAccount(context.Background(), spec); err == nil {
			t.Fatal("accepted invalid specification")
		}
		if err := c.ReconcileServiceAccount(context.Background(), spec, "uuid", "subject", true); err == nil {
			t.Fatal("accepted invalid reconciliation")
		}
	}
}

func TestAdministratorTokenCancellationAndFailure(t *testing.T) {
	c := &Client{tokenGate: make(chan struct{}, 1)}
	c.tokenGate <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.adminToken(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("token refresh ignored cancellation")
	}
}
