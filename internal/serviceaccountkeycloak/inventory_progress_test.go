package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/segmentio/ksuid"
)

// A provider read fault must not keep independent clients out of saved cleanup.
// This regression is the next acceptance gate for bounded provider discovery.
func TestGatewayInventoryRegistersPastFailedClientRead(t *testing.T) {
	gatewayID := ksuid.New().String()
	accounts := []string{ksuid.New().String(), ksuid.New().String()}
	clients := []kcClient{}
	for i, id := range accounts {
		clients = append(clients, kcClient{ID: []string{"unavailable", "later"}[i], ClientID: "hs-sa-" + gatewayID + "-" + id, Enabled: true, Attributes: map[string]string{managedAttribute: "true", gatewayIDAttribute: gatewayID, serviceAccountIDAttribute: id}})
	}
	var requests atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		switch r.URL.Path {
		case "/realms/test/protocol/openid-connect/token":
			_, _ = w.Write([]byte(`{"access_token":"admin-token","expires_in":300,"token_type":"Bearer"}`))
		case "/admin/realms/test/clients":
			_ = json.NewEncoder(w).Encode(clients)
		case "/admin/realms/test/clients/unavailable":
			w.WriteHeader(http.StatusServiceUnavailable)
		case "/admin/realms/test/clients/later":
			if r.Method != http.MethodGet {
				t.Error("discovery changed provider state")
				w.WriteHeader(500)
				return
			}
			_ = json.NewEncoder(w).Encode(clients[1])
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
	client, err := NewClient(Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca, AccountJournal: journals})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	version, err := client.GatewayInventorySource(gatewayID)
	if err != nil {
		t.Fatal(err)
	}
	before := requests.Load()
	if _, err := client.GatewayInventoryPage(context.Background(), gatewayID, "changed-source", "", 20); err == nil {
		t.Fatal("changed source reached query")
	}
	if _, err := client.PrepareGatewayInventoryCandidate(context.Background(), gatewayID, "changed-source", "later"); err == nil {
		t.Fatal("changed source reached preparation")
	}
	if requests.Load() != before {
		t.Fatal("source mismatch reached provider")
	}
	var checkpoint runtime.Checkpoint
	access := runtime.CheckpointAccess{
		Load: func(context.Context) (runtime.Checkpoint, error) { return checkpoint, nil },
		Save: func(_ context.Context, prior int64, data string) error {
			if prior != checkpoint.Version {
				return errors.New("checkpoint conflict")
			}
			checkpoint = runtime.Checkpoint{Version: prior + 1, After: data}
			return nil
		},
	}
	_, err = runtime.ScanCycle(context.Background(), version, access, func(ctx context.Context, after string, limit int) (runtime.CursorPage[string], error) {
		return client.GatewayInventoryPage(ctx, gatewayID, version, after, limit)
	}, func(ctx context.Context, id string) error {
		_, err := client.PrepareGatewayInventoryCandidate(ctx, gatewayID, version, id)
		return err
	}, func(error) bool { return true }, runtime.ScanOptions{PageSize: 20, MaxPages: 1, PageTimeout: time.Second}, runtime.ObservationOptions{WorkTimeout: time.Second, CommitTimeout: time.Second})
	if !errors.Is(err, runtime.ErrCycleFailed) {
		t.Fatal("incomplete discovery reported success", err)
	}
	journal, err := journals(gatewayID, accounts[1], true)
	if err != nil {
		t.Fatal(err)
	}
	saved, err := journal.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var record struct {
		Closed bool `json:"closed"`
	}
	if saved.Version() == 0 || json.Unmarshal(saved.Reveal(), &record) != nil || !record.Closed {
		t.Fatal("failed client read blocked the later cleanup journal")
	}
}
