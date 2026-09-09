package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/segmentio/ksuid"
)

func TestGatewayCleanupRetriesAfterPartialDisable(t *testing.T) {
	gatewayID := ksuid.New().String()
	var mu sync.Mutex
	clients := map[string]kcClient{}
	names := []string{"first", "second", "third", "foreign"}
	for _, name := range names {
		parent := gatewayID
		if name == "foreign" {
			parent = ksuid.New().String()
		}
		account := ksuid.New().String()
		clients[name] = kcClient{ID: name, ClientID: "hs-sa-" + parent + "-" + account, Enabled: true, Attributes: map[string]string{managedAttribute: "true", gatewayIDAttribute: parent, serviceAccountIDAttribute: account}}
	}
	fail := true
	deletes := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/realms/test/protocol/openid-connect/token" {
			_, _ = w.Write([]byte(`{"access_token":"admin-token","expires_in":300}`))
			return
		}
		if r.URL.Path == "/admin/realms/test/clients" {
			list := []kcClient{}
			for _, name := range names {
				if c, ok := clients[name]; ok {
					list = append(list, c)
				}
			}
			_ = json.NewEncoder(w).Encode(list)
			return
		}
		name := strings.TrimPrefix(r.URL.Path, "/admin/realms/test/clients/")
		current, ok := clients[name]
		if !ok {
			w.WriteHeader(404)
			return
		}
		switch r.Method {
		case "GET":
			_ = json.NewEncoder(w).Encode(current)
		case "PUT":
			if name == "foreign" {
				t.Error("foreign client was changed")
				w.WriteHeader(500)
				return
			}
			if fail && name == "second" {
				w.WriteHeader(503)
				_, _ = w.Write([]byte("private provider details"))
				return
			}
			var next kcClient
			if json.NewDecoder(r.Body).Decode(&next) != nil || next.Enabled {
				t.Error("cleanup enabled a client")
				w.WriteHeader(500)
				return
			}
			clients[name] = next
			w.WriteHeader(204)
		case "DELETE":
			if name == "foreign" {
				t.Error("foreign client was removed")
				w.WriteHeader(500)
				return
			}
			for _, c := range clients {
				if c.Attributes[gatewayIDAttribute] == gatewayID && c.Enabled {
					t.Error("client removed before all identities were disabled")
				}
			}
			delete(clients, name)
			deletes++
			w.WriteHeader(204)
		default:
			t.Error("unexpected provider operation")
			w.WriteHeader(500)
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
	client, err := NewClient(Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.DeleteGatewayServiceAccounts(context.Background(), gatewayID); err == nil || strings.Contains(err.Error(), "private") {
		t.Fatal("partial provider failure was lost or exposed", err)
	}
	mu.Lock()
	if deletes != 0 || clients["first"].Enabled || !clients["second"].Enabled {
		t.Error("partial disable state is wrong")
	}
	fail = false
	mu.Unlock()
	if err := client.DeleteGatewayServiceAccounts(context.Background(), gatewayID); err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteGatewayServiceAccounts(context.Background(), gatewayID); err != nil {
		t.Fatal("repeat cleanup", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if deletes != 3 || len(clients) != 1 || !clients["foreign"].Enabled {
		t.Fatal("cleanup changed another Gateway or repeated deletion")
	}
}
