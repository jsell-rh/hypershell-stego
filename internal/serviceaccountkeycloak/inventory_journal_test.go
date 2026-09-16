package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/segmentio/ksuid"
)

func TestGatewayInventoryRequiresIndependentJournalRecovery(t *testing.T) {
	gatewayID, accountID := ksuid.New().String(), ksuid.New().String()
	orphan := kcClient{ID: "retained-orphan", ClientID: "hs-sa-" + gatewayID + "-" + accountID, Enabled: false,
		Attributes: map[string]string{managedAttribute: "true", gatewayIDAttribute: gatewayID, serviceAccountIDAttribute: accountID}}
	var mu sync.Mutex
	exists, failDelete, omit := true, true, false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/realms/test/protocol/openid-connect/token" {
			_, _ = w.Write([]byte(`{"access_token":"admin-token","expires_in":300,"token_type":"Bearer"}`))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients" {
			rows := []kcClient{}
			if exists && !omit {
				rows = append(rows, orphan)
			}
			_ = json.NewEncoder(w).Encode(rows)
			return
		}
		if r.URL.Path != "/admin/realms/test/clients/"+orphan.ID || !exists {
			w.WriteHeader(404)
			return
		}
		switch r.Method {
		case "GET":
			_ = json.NewEncoder(w).Encode(orphan)
		case "DELETE":
			if failDelete {
				w.WriteHeader(503)
				return
			}
			exists = false
			w.WriteHeader(204)
		default:
			t.Error("unexpected provider operation")
			w.WriteHeader(500)
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
	journals := testAccountJournals(t)
	options := Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca, AccountJournal: journals}
	client, err := NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	if err := client.DeleteGatewayServiceAccounts(context.Background(), gatewayID); err == nil {
		t.Fatal("provider fault did not interrupt cleanup")
	}
	journal, err := journals(gatewayID, accountID, true)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := journal.Load(context.Background())
	var record struct {
		Closed bool `json:"closed"`
	}
	if err != nil || saved.Version() == 0 || json.Unmarshal(saved.Reveal(), &record) != nil || !record.Closed {
		t.Fatal("interrupted cleanup did not retain its closure journal", err)
	}
	client.Close()
	client, err = NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	mu.Lock()
	failDelete, omit = false, true
	mu.Unlock()
	// Inventory is one recovery source. The application must separately scan
	// retained journal IDs before it can certify complete Gateway cleanup.
	err = client.DeleteGatewayServiceAccounts(context.Background(), gatewayID)
	mu.Lock()
	omitted := err == nil && exists
	mu.Unlock()
	if !omitted {
		t.Fatal("fixture did not preserve an omitted journaled orphan", err)
	}
	// The retained identity remains sufficient when the list omits the client.
	if err := client.DeleteManagedServiceAccount(context.Background(), gatewayID, accountID); err != nil {
		t.Fatal("known identity could not recover cleanup", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if exists {
		t.Fatal("known identity cleanup left the orphan")
	}
}
