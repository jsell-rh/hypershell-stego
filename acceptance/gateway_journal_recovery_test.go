package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type journalCleanupProvider struct {
	*accountProvider
	client *serviceaccountkeycloak.Client
}

func (p *journalCleanupProvider) Delete(ctx context.Context, gatewayID, id, uuid string) error {
	if uuid == "" {
		return p.client.DeleteManagedServiceAccount(ctx, gatewayID, id)
	}
	return p.client.DeleteServiceAccount(ctx, uuid, gatewayID, id)
}
func (p *journalCleanupProvider) DeleteGateway(ctx context.Context, id string) error {
	return p.client.DeleteGatewayServiceAccounts(ctx, id)
}

// This test composes the application recovery service, generated PostgreSQL
// storage, encrypted journals, and generated provider lifecycle. The HTTPS
// provider is a fault fixture. Store and client reconstruction are explicit;
// this test does not claim a process or database-server restart.
func TestGatewayCleanupRecoversJournalOmittedByProvider(t *testing.T) {
	f := database(t)
	_, gateway := accountService(t, f, newAccountProvider())
	ctx := context.Background()
	if err := f.storage.Delete(ctx, "Gateway", gateway.ID); err != nil {
		t.Fatal(err)
	}
	accountID := ksuid.New().String()
	orphan := map[string]any{"id": "retained-orphan", "clientId": "hs-sa-" + gateway.ID + "-" + accountID, "enabled": false, "attributes": map[string]string{
		"hypershell.service-account": "true", "hypershell.gateway-id": gateway.ID, "hypershell.service-account-id": accountID,
	}}
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
			rows := []map[string]any{}
			if exists && !omit {
				rows = append(rows, orphan)
			}
			_ = json.NewEncoder(w).Encode(rows)
			return
		}
		if r.URL.Path != "/admin/realms/test/clients/retained-orphan" || !exists {
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
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatal(err)
	}
	defer clear(master)
	makeClient := func(store *model.Store) *serviceaccountkeycloak.Client {
		t.Helper()
		protector, err := runtime.NewStateProtector([][]byte{bytes.Clone(master)})
		if err != nil {
			t.Fatal(err)
		}
		client, err := serviceaccountkeycloak.NewClient(serviceaccountkeycloak.Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca,
			AccountJournal: func(gatewayID, id string, cleanup bool) (*runtime.StateJournal, error) {
				if gatewayID != gateway.ID || id != accountID || !cleanup {
					t.Error("journal escaped cleanup scope")
				}
				scope := gateways.AccountProviderStateScope(gatewayID)
				return runtime.NewStateJournal(protector, runtime.StateKey{Instance: "omitted-provider", Entity: "ServiceAccount", ResourceID: id, Scope: scope}, runtime.StatePersistence{
					Load: func(ctx context.Context) (runtime.SealedStateRecord, error) {
						r, err := store.LoadResourceState(ctx, "ServiceAccount", id, scope)
						return runtime.SealedStateRecord{Version: r.Version, Data: r.Data}, err
					},
					Save: func(ctx context.Context, version int64, data []byte) (runtime.SealedStateRecord, error) {
						var result runtime.SealedStateRecord
						err := store.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
							saved, err := tx.(storage.ResourceStateStore).SaveResourceState(ctx, "ServiceAccount", id, scope, version, data)
							result = runtime.SealedStateRecord{Version: saved.Version, Data: saved.Data}
							return err
						})
						return result, err
					},
				}, gateways.MaxGatewayProviderStateBytes)
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return client
	}
	client := makeClient(f.storage)
	service, err := serviceaccounts.New(f.storage, &journalCleanupProvider{accountProvider: newAccountProvider(), client: client})
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := service.RecoverGatewayCleanup(ctx, gateway.ID); err == nil || complete {
		t.Fatal("provider failure permitted completion", complete, err)
	}
	saved, err := f.storage.LoadResourceState(ctx, "ServiceAccount", accountID, gateways.AccountProviderStateScope(gateway.ID))
	if err != nil || saved.Version == 0 || len(saved.Data) == 0 || bytes.Contains(saved.Data, []byte("retained-orphan")) {
		t.Fatal("cleanup did not save encrypted state", err)
	}
	var rows int
	if err := f.db.QueryRow("SELECT count(*) FROM service_accounts WHERE id=$1", accountID).Scan(&rows); err != nil || rows != 0 {
		t.Fatal("fixture has an account row", rows, err)
	}
	client.Close()
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: f.db}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := model.NewStore(orm)
	if err != nil {
		t.Fatal(err)
	}
	client = makeClient(restarted)
	defer client.Close()
	service, err = serviceaccounts.New(restarted, &journalCleanupProvider{accountProvider: newAccountProvider(), client: client})
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	omit = true
	mu.Unlock()
	// Even an empty provider list cannot hide a failed known obligation.
	if complete, err := service.RecoverGatewayCleanup(ctx, gateway.ID); err == nil || complete {
		t.Fatal("omitted failed journal permitted completion", complete, err)
	}
	mu.Lock()
	failDelete = false
	mu.Unlock()
	if complete, err := service.RecoverGatewayCleanup(ctx, gateway.ID); err != nil || !complete {
		t.Fatal("retained journal did not recover", complete, err)
	}
	mu.Lock()
	remaining := exists
	mu.Unlock()
	if remaining {
		t.Fatal("cleanup completed while the omitted provider client existed")
	}
	if complete, err := service.RecoverGatewayCleanup(ctx, gateway.ID); err != nil || !complete {
		t.Fatal("repeated journal cleanup failed", complete, err)
	}
	t.Log("A saved encrypted journal without an account row prevented false completion and recovered the omitted provider client after store and client reconstruction")
}
