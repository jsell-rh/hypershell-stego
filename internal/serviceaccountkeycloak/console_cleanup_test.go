package serviceaccountkeycloak

import (
	"context"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	provider "github.com/jsell-rh/hypershell-stego/out/keycloak"
	"github.com/segmentio/ksuid"
)

func TestGatewayClosureContinuesAfterConsoleFailure(t *testing.T) {
	for _, failure := range []string{"journal", "provider"} {
		t.Run(failure, func(t *testing.T) {
			id := ksuid.New().String()
			var fault atomic.Bool
			fault.Store(true)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch {
				case r.URL.Path == "/realms/test/protocol/openid-connect/token":
					_, _ = w.Write([]byte(`{"access_token":"test-admin-token","expires_in":300,"token_type":"Bearer"}`))
				case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients":
					if fault.Load() && failure == "provider" && strings.HasPrefix(r.URL.Query().Get("clientId"), "hs-console-") {
						w.WriteHeader(http.StatusServiceUnavailable)
					} else {
						_, _ = w.Write([]byte(`[]`))
					}
				case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/admin/realms/test/clients/"):
					w.WriteHeader(http.StatusNotFound)
				default:
					t.Errorf("unexpected provider change: %s %s", r.Method, r.URL.Path)
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
			native, console := testGatewayJournals(t), testGatewayJournals(t)
			unavailable := errors.New("console journal is unavailable")
			options := Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca, GatewayJournal: native,
				ConsoleJournal: func(id string, revision int64, cleanup bool) (*runtime.StateJournal, error) {
					if fault.Load() && failure == "journal" {
						return nil, unavailable
					}
					return console(id, revision, cleanup)
				},
			}
			client, err := NewClient(options)
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			err = client.DeleteGateway(ctx, id, 1)
			if err == nil || (failure == "journal" && !errors.Is(err, unavailable)) {
				t.Fatal("console failure was lost", err)
			}
			// A failed console cleanup must not leave the native identity open to repair.
			restarted, err := NewClient(options)
			if err != nil {
				t.Fatal(err)
			}
			defer restarted.Close()
			if _, err = restarted.EnsureGateway(ctx, id, "gateway", 1); !errors.Is(err, provider.ErrClientClosed) {
				t.Fatal("native closure was not retained across restart", err)
			}
			fault.Store(false)
			if err := restarted.DeleteGateway(ctx, id, 1); err != nil {
				t.Fatal("cleanup did not recover", err)
			}
			if err := restarted.DeleteGateway(ctx, id, 1); err != nil {
				t.Fatal("repeated cleanup failed", err)
			}
			lifecycle, err := restarted.consoleLifecycle(id, 1, false)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := lifecycle.Reconcile(ctx, func(provider.ClientBinding) (provider.BrowserAccessPolicy, error) {
				t.Error("closed console requested policy")
				return provider.BrowserAccessPolicy{}, nil
			}); !errors.Is(err, provider.ErrClientClosed) {
				t.Fatal("console closure was not retained", err)
			}
		})
	}
}
