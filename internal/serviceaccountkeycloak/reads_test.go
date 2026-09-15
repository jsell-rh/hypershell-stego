package serviceaccountkeycloak

import (
	"context"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// The adapter must not turn a malformed read into absence or select one of
// several matching clients. Those results can otherwise lead to unsafe writes.
func TestClientReadsRequireExactIdentity(t *testing.T) {
	for _, item := range []struct {
		name, body string
		lookup     bool
	}{
		{"ambiguous lookup", `[{"id":"first","clientId":"worker"},{"id":"second","clientId":"worker"}]`, true},
		{"foreign lookup", `[{"id":"first","clientId":"foreign"}]`, true},
		{"null lookup", `null`, true},
		{"duplicate field", `[{"id":"first","id":"second","clientId":"worker"}]`, true},
		{"wrong provider ID", `{"id":"other","clientId":"worker"}`, false},
		{"missing client ID", `{"id":"saved"}`, false},
	} {
		t.Run(item.name, func(t *testing.T) {
			var writes atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/realms/test/protocol/openid-connect/token" {
					w.Write([]byte(`{"access_token":"test-admin-token","expires_in":300,"token_type":"Bearer"}`))
					return
				}
				if r.Method != http.MethodGet {
					writes.Add(1)
					w.WriteHeader(500)
					return
				}
				if item.lookup && (r.URL.Query().Get("max") != "2" || r.URL.Query().Get("search") != "false") {
					t.Error("lookup is not bounded and exact")
				}
				w.Write([]byte(item.body))
			}))
			defer server.Close()
			dir := t.TempDir()
			ca, secret := filepath.Join(dir, "ca"), filepath.Join(dir, "secret")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(secret, []byte("private-test-secret"), 0600); err != nil {
				t.Fatal(err)
			}
			client, err := NewClient(Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			if item.lookup {
				_, err = client.clientUUID(context.Background(), "worker")
			} else {
				_, err = client.getClient(context.Background(), "saved")
			}
			if err == nil || strings.Contains(err.Error(), "private-test-secret") || writes.Load() != 0 {
				t.Fatal("unsafe read was accepted or exposed credentials")
			}
		})
	}
}
