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
)

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
	for _, ids := range [][2]string{{"", "account"}, {"gateway", ""}, {"other", "account"}, {"gateway", "other"}} {
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
