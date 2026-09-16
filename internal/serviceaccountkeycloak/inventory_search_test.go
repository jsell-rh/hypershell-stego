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
	"strings"
	"sync"
	"testing"

	"github.com/segmentio/ksuid"
)

func TestManagedInventoryUsesGatewaySearchAndChecksOwnership(t *testing.T) {
	gatewayID, accountID := ksuid.New().String(), ksuid.New().String()
	prefix := "hs-sa-" + gatewayID + "-"
	owned := kcClient{ID: "owned", ClientID: prefix + accountID, Attributes: map[string]string{managedAttribute: "true", gatewayIDAttribute: gatewayID, serviceAccountIDAttribute: accountID}}
	foreign := kcClient{ID: "foreign", ClientID: "other-" + prefix + accountID, Attributes: map[string]string{managedAttribute: "true", gatewayIDAttribute: ksuid.New().String(), serviceAccountIDAttribute: accountID}}
	var mu sync.Mutex
	calls, hydrates := 0, 0
	denied, wrongName, global := false, false, false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/realms/test/protocol/openid-connect/token" {
			_, _ = w.Write([]byte(`{"access_token":"admin-token","expires_in":300,"token_type":"Bearer"}`))
			return
		}
		if r.Method != "GET" {
			t.Error("inventory attempted a mutation")
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == "/admin/realms/test/clients" {
			calls++
			q := r.URL.Query()
			if q.Get("first") != "0" || q.Get("max") != "100" {
				t.Error("inventory page bounds differ")
			}
			if global {
				if q.Has("clientId") || q.Has("search") {
					t.Error("global inventory was narrowed")
				}
			} else if q.Get("clientId") != prefix || q.Get("search") != "true" || q.Has("q") {
				t.Error("Gateway inventory did not use the bounded common name query")
			}
			if denied {
				w.WriteHeader(403)
				return
			}
			// Provider substring matches are only candidates. The second name includes
			// the fragment but belongs to another Gateway. The list omits attributes.
			candidates := []map[string]string{{"id": owned.ID, "clientId": owned.ClientID}}
			if !global {
				candidates = append(candidates, map[string]string{"id": foreign.ID, "clientId": foreign.ClientID})
			}
			_ = json.NewEncoder(w).Encode(candidates)
			return
		}
		hydrates++
		switch r.URL.Path {
		case "/admin/realms/test/clients/owned":
			value := owned
			if wrongName {
				value.ClientID = strings.ToUpper(prefix) + accountID
			}
			_ = json.NewEncoder(w).Encode(value)
		case "/admin/realms/test/clients/foreign":
			_ = json.NewEncoder(w).Encode(foreign)
		default:
			t.Error("unexpected inventory resource")
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	ca, secret := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "secret")
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
	rows, err := client.ListManagedClients(context.Background(), gatewayID)
	if err != nil || len(rows) != 1 || rows[0].UUID != owned.ID || rows[0].GatewayID != gatewayID {
		t.Fatal("candidate ownership differs", err)
	}
	mu.Lock()
	if calls != 1 || hydrates != 2 {
		t.Error("inventory did not inspect each current candidate")
	}
	denied = true
	before := calls
	mu.Unlock()
	if _, err := client.ListManagedClients(context.Background(), gatewayID); err == nil {
		t.Fatal("denied search was accepted")
	}
	mu.Lock()
	if calls != before+1 {
		t.Error("denied search retried another inventory path")
	}
	denied = false
	wrongName = true
	mu.Unlock()
	if _, err := client.ListManagedClients(context.Background(), gatewayID); !errors.Is(err, ErrNotManaged) {
		t.Fatal("noncanonical name was adopted", err)
	}
	mu.Lock()
	before = calls
	mu.Unlock()
	for _, id := range []string{"not-an-id", ksuid.Nil.String(), gatewayID + "%"} {
		if _, err := client.ListManagedClients(context.Background(), id); !errors.Is(err, ErrNotManaged) {
			t.Fatal("invalid Gateway query was accepted", err)
		}
	}
	mu.Lock()
	if calls != before {
		t.Error("invalid Gateway query reached the provider")
	}
	wrongName = false
	global = true
	mu.Unlock()
	rows, err = client.ListManagedClients(context.Background(), "")
	if err != nil || len(rows) != 1 || rows[0].UUID != owned.ID {
		t.Fatal("authorized global inventory changed", err)
	}
}
