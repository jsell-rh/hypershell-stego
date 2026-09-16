package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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

func TestGatewayInventoryRecoveryAcrossReadFailureAndPageShift(t *testing.T) {
	f := database(t)
	_, gateway := accountService(t, f, newAccountProvider())
	ctx := context.Background()
	if err := f.storage.Delete(ctx, "Gateway", gateway.ID); err != nil {
		t.Fatal(err)
	}
	ids := []string{}
	for i := 0; i < 23; i++ {
		ids = append(ids, ksuid.New().String())
	}
	sort.Strings(ids)
	clients := map[string]map[string]any{}
	for _, id := range ids {
		clients[id] = map[string]any{"id": id, "clientId": "hs-sa-" + gateway.ID + "-" + id, "enabled": false, "attributes": map[string]string{"hypershell.service-account": "true", "hypershell.gateway-id": gateway.ID, "hypershell.service-account-id": id}}
	}
	clients["foreign"] = map[string]any{"id": "foreign", "clientId": "hs-sa-" + gateway.ID + "-other", "enabled": false, "attributes": map[string]string{"hypershell.service-account": "true", "hypershell.gateway-id": ksuid.New().String(), "hypershell.service-account-id": ksuid.New().String()}}
	var mu sync.Mutex
	failRead := true
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path == "/realms/test/protocol/openid-connect/token" {
			_, _ = w.Write([]byte(`{"access_token":"admin-token","expires_in":300,"token_type":"Bearer"}`))
			return
		}
		if r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients" {
			q := r.URL.Query()
			if q.Get("clientId") != "hs-sa-"+gateway.ID+"-" || q.Get("search") != "true" {
				t.Error("query escaped Gateway")
				w.WriteHeader(400)
				return
			}
			first, _ := strconv.Atoi(q.Get("first"))
			limit, _ := strconv.Atoi(q.Get("max"))
			rows := []map[string]any{}
			for _, v := range clients {
				rows = append(rows, v)
			}
			sort.Slice(rows, func(i, j int) bool { return rows[i]["clientId"].(string) < rows[j]["clientId"].(string) })
			page := []map[string]any{}
			for i := first; i < min(first+limit, len(rows)); i++ {
				page = append(page, map[string]any{"id": rows[i]["id"], "clientId": rows[i]["clientId"]})
			}
			_ = json.NewEncoder(w).Encode(page)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, "/admin/realms/test/clients/")
		value, ok := clients[id]
		if !ok {
			w.WriteHeader(404)
			return
		}
		if r.Method == "GET" {
			if id == ids[0] && failRead {
				w.WriteHeader(503)
				return
			}
			_ = json.NewEncoder(w).Encode(value)
			return
		}
		if r.Method == "DELETE" && id != "foreign" {
			delete(clients, id)
			w.WriteHeader(204)
			return
		}
		t.Error("unexpected or foreign provider mutation")
		w.WriteHeader(500)
	}))
	defer server.Close()
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatal(err)
	}
	defer clear(master)
	makeService := func(store *model.Store) (*serviceaccounts.Service, *serviceaccountkeycloak.Client) {
		client := inventorySQLClient(t, store, gateway.ID, server, master)
		service, err := serviceaccounts.New(store, &journalCleanupProvider{accountProvider: newAccountProvider(), client: client})
		if err != nil {
			t.Fatal(err)
		}
		return service, client
	}
	service, client := makeService(f.storage)
	if complete, err := service.RecoverGatewayCleanup(ctx, gateway.ID); err == nil || complete {
		t.Fatal("failed first read permitted completion", complete, err)
	}
	checkpoint, err := f.storage.LoadCheckpoint(ctx, "Gateway", gateway.ID, "gateway-provider-inventory")
	if err != nil {
		t.Fatal(err)
	}
	state, err := runtime.DecodeCycle(checkpoint.After)
	if err != nil || state.After != "1.20" || state.Complete || !state.Failed {
		t.Fatal("first page did not retain cursor and failure", state, err)
	}
	saved, err := f.storage.LoadResourceState(ctx, "ServiceAccount", ids[19], gateways.AccountProviderStateScope(gateway.ID))
	if err != nil || saved.Version == 0 {
		t.Fatal("first read blocked later closure", err)
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
	service, client = makeService(restarted)
	defer client.Close()
	for pass := 0; pass < 2; pass++ {
		if complete, err := service.RecoverGatewayCleanup(ctx, gateway.ID); complete || err == nil {
			t.Fatal("failed discovery cycle permitted completion", pass, complete, err)
		}
	}
	saved, err = restarted.LoadResourceState(ctx, "ServiceAccount", ids[22], gateways.AccountProviderStateScope(gateway.ID))
	if err != nil || saved.Version == 0 {
		t.Fatal("page shift hid the tail client after restart", err)
	}
	mu.Lock()
	_, prefixRemains := clients[ids[19]]
	_, foreignRemains := clients["foreign"]
	failRead = false
	mu.Unlock()
	if prefixRemains || !foreignRemains {
		t.Fatal("independent cleanup did not remove prefix or changed foreign client")
	}
	complete := false
	for pass := 0; pass < 8 && !complete; pass++ {
		complete, err = service.RecoverGatewayCleanup(ctx, gateway.ID)
	}
	if !complete || err != nil {
		t.Fatal("cleanup did not converge after read recovery", complete, err)
	}
	mu.Lock()
	remaining := len(clients)
	_, foreignRemains = clients["foreign"]
	mu.Unlock()
	if remaining != 1 || !foreignRemains {
		t.Fatal("cleanup completed with owned clients or removed foreign client")
	}
	membership, err := restarted.LoadResourceStateScope(ctx, "ServiceAccount", gateways.AccountProviderStateScope(gateway.ID))
	if err != nil || !membership.Sealed {
		t.Fatal("completion did not seal journal registration", err)
	}
}

func inventorySQLClient(t *testing.T, store *model.Store, gatewayID string, server *httptest.Server, master []byte) *serviceaccountkeycloak.Client {
	t.Helper()
	dir := t.TempDir()
	ca, secret := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "secret")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("test-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	protector, err := runtime.NewStateProtector([][]byte{bytes.Clone(master)})
	if err != nil {
		t.Fatal(err)
	}
	client, err := serviceaccountkeycloak.NewClient(serviceaccountkeycloak.Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca, AccountJournal: func(parent, id string, cleanup bool) (*runtime.StateJournal, error) {
		if parent != gatewayID || !cleanup {
			t.Error("journal escaped Gateway cleanup")
		}
		scope := gateways.AccountProviderStateScope(parent)
		return runtime.NewStateJournal(protector, runtime.StateKey{Instance: "inventory-recovery", Entity: "ServiceAccount", ResourceID: id, Scope: scope}, runtime.StatePersistence{
			Load: func(ctx context.Context) (runtime.SealedStateRecord, error) {
				r, err := store.LoadResourceState(ctx, "ServiceAccount", id, scope)
				return runtime.SealedStateRecord{Version: r.Version, Data: r.Data}, err
			},
			Save: func(ctx context.Context, version int64, data []byte) (runtime.SealedStateRecord, error) {
				var result runtime.SealedStateRecord
				err := store.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
					r, err := tx.(storage.ResourceStateStore).SaveResourceState(ctx, "ServiceAccount", id, scope, version, data)
					result = runtime.SealedStateRecord{Version: r.Version, Data: r.Data}
					return err
				})
				return result, err
			},
		}, gateways.MaxGatewayProviderStateBytes)
	}})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

type inventoryBoundaryProvider struct {
	*accountProvider
	cursors []string
}

func (p *inventoryBoundaryProvider) InventoryPage(_ context.Context, _ string, _ string, after string, _ int) (runtime.CursorPage[string], error) {
	p.cursors = append(p.cursors, after)
	if after == "1.10000" {
		return runtime.CursorPage[string]{}, runtime.ErrScanWindowLimit
	}
	return runtime.CursorPage[string]{}, nil
}
func TestGatewayInventoryLimitRequiresAnotherFullCycle(t *testing.T) {
	f := database(t)
	_, gateway := accountService(t, f, newAccountProvider())
	ctx := context.Background()
	if err := f.storage.Delete(ctx, "Gateway", gateway.ID); err != nil {
		t.Fatal(err)
	}
	value, err := f.storage.GetRetained(ctx, "Gateway", gateway.ID)
	if err != nil {
		t.Fatal(err)
	}
	current := value.(model.Gateway)
	provider := &inventoryBoundaryProvider{accountProvider: newAccountProvider()}
	source, err := provider.InventorySource(ctx, gateway.ID)
	if err != nil {
		t.Fatal(err)
	}
	version := strconv.FormatInt(current.ResourceGeneration, 10) + ":" + source + ":inventory-v1"
	encoded, err := runtime.EncodeCycle(runtime.CycleState{Source: version, After: "1.10000"})
	if err != nil {
		t.Fatal(err)
	}
	err = f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		_, err := tx.(storage.CheckpointStore).SaveCheckpoint(ctx, "Gateway", gateway.ID, "gateway-provider-inventory", 0, encoded)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	service, err := serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := service.RecoverGatewayCleanup(ctx, gateway.ID); complete || !errors.Is(err, runtime.ErrScanWindowLimit) {
		t.Fatal("inventory limit became cleanup completion", complete, err)
	}
	saved, err := f.storage.LoadCheckpoint(ctx, "Gateway", gateway.ID, "gateway-provider-inventory")
	if err != nil {
		t.Fatal(err)
	}
	state, err := runtime.DecodeCycle(saved.After)
	if err != nil || !state.Failed || !state.Complete {
		t.Fatal("limit did not retain a failed cycle", state, err)
	}
	if complete, err := service.RecoverGatewayCleanup(ctx, gateway.ID); !complete || err != nil {
		t.Fatal("later full scan did not complete", complete, err)
	}
	if len(provider.cursors) != 2 || provider.cursors[0] != "1.10000" || provider.cursors[1] != "" {
		t.Fatal("limit did not restart at the beginning", provider.cursors)
	}
}
