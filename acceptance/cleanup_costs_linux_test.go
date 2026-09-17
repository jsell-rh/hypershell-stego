package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

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

// This benchmark requires the bounded hosted CI workflow and one iteration.
// It measures SQL, protected journals, the common HTTPS client, and recovery.
// The remote Keycloak server is a controlled protocol fixture.
func BenchmarkGatewayAccountJournalCleanup(b *testing.B) {
	if os.Getenv("STEGO_CAPACITY_CI") != "1" || b.N != 1 {
		b.Fatal("use the bounded cleanup CI workflow with -benchtime=1x")
	}
	const size = 1000
	f := database(b)
	_, gateway := accountService(b, f, newAccountProvider())
	setup, stopSetup := context.WithTimeout(context.Background(), 2*time.Minute)
	defer stopSetup()
	var creator string
	if err := f.db.QueryRowContext(setup, "SELECT user_id FROM role_bindings WHERE gateway_id=$1", gateway.ID).Scan(&creator); err != nil {
		b.Fatal(err)
	}
	ids := make([]string, 2*size)
	clients := map[string]map[string]any{"foreign": {"id": "foreign", "clientId": "unrelated", "enabled": false}}
	for i := range ids {
		id := ksuid.New().String()
		ids[i] = id
		providerID := "client-" + id
		clientID := "hs-sa-" + gateway.ID + "-" + id
		clients[providerID] = map[string]any{"id": providerID, "clientId": clientID, "enabled": true,
			"attributes": map[string]string{"hypershell.service-account": "true", "hypershell.gateway-id": gateway.ID, "hypershell.service-account-id": id}}
		if i < size {
			name := fmt.Sprintf("cleanup-%d", i)
			row := model.ServiceAccount{Meta: model.Meta{ID: id}, GatewayID: gateway.ID, Name: name, ActiveName: &name, Active: true, CredentialType: "client_secret", Role: serviceaccounts.RoleUser, Status: "ready", CreatedByUserID: creator, ClientID: clientID, ClientUuid: providerID, ExpiresAt: time.Now().Add(time.Hour)}
			if err := f.storage.Create(setup, "ServiceAccount", row); err != nil {
				b.Fatal(err)
			}
		}
	}
	var mu sync.Mutex
	deletes := map[string]int{}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method == "POST" && r.URL.Path == "/realms/test/protocol/openid-connect/token" {
			_, _ = w.Write([]byte(`{"access_token":"cleanup-admin","expires_in":300,"token_type":"Bearer"}`))
			return
		}
		if r.Header.Get("Authorization") != "Bearer cleanup-admin" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients" {
			query := r.URL.Query().Get("clientId")
			var selected []string
			for id, row := range clients {
				if strings.Contains(row["clientId"].(string), query) {
					selected = append(selected, id)
				}
			}
			sort.Strings(selected)
			first, _ := strconv.Atoi(r.URL.Query().Get("first"))
			limit, _ := strconv.Atoi(r.URL.Query().Get("max"))
			if first < 0 || limit < 1 || limit > 101 {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			rows := []map[string]any{}
			for i := first; i < len(selected) && i < first+limit; i++ {
				rows = append(rows, clients[selected[i]])
			}
			_ = json.NewEncoder(w).Encode(rows)
			return
		}
		id, ok := strings.CutPrefix(r.URL.Path, "/admin/realms/test/clients/")
		row := clients[id]
		if !ok || row == nil {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		switch r.Method {
		case "GET":
			_ = json.NewEncoder(w).Encode(row)
		case "DELETE":
			if id == "foreign" {
				b.Error("cleanup reached a foreign client")
				w.WriteHeader(http.StatusForbidden)
				return
			}
			deletes[id]++
			delete(clients, id)
			w.WriteHeader(http.StatusNoContent)
		default:
			b.Error("unexpected provider operation")
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()
	dir := b.TempDir()
	ca, secret := filepath.Join(dir, "ca.pem"), filepath.Join(dir, "secret")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		b.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("cleanup-test-secret"), 0600); err != nil {
		b.Fatal(err)
	}
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		b.Fatal(err)
	}
	defer clear(master)
	scope := gateways.AccountProviderStateScope(gateway.ID)
	store := f.storage
	journal := func(id string) (*runtime.StateJournal, error) {
		protector, err := runtime.NewStateProtector([][]byte{bytes.Clone(master)})
		if err != nil {
			return nil, err
		}
		return runtime.NewStateJournal(protector, runtime.StateKey{Instance: "cleanup-costs", Entity: "ServiceAccount", ResourceID: id, Scope: scope}, runtime.StatePersistence{
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
	}
	makeClient := func() *serviceaccountkeycloak.Client {
		c, err := serviceaccountkeycloak.NewClient(serviceaccountkeycloak.Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca,
			AccountJournal: func(gatewayID, id string, cleanup bool) (*runtime.StateJournal, error) {
				if gatewayID != gateway.ID || !cleanup {
					return nil, errors.New("journal escaped the cleanup scope")
				}
				return journal(id)
			}})
		if err != nil {
			b.Fatal(err)
		}
		return c
	}
	client := makeClient()
	defer func() { client.Close() }()
	if err := f.service.Delete(setup, principal("alice"), gateway.ID); err != nil {
		b.Fatal(err)
	}
	version, err := client.GatewayInventorySource(gateway.ID)
	if err != nil {
		b.Fatal(err)
	}
	for _, id := range ids {
		if owned, err := client.PrepareGatewayInventoryCandidate(setup, gateway.ID, version, "client-"+id); err != nil || !owned {
			b.Fatal("encrypted cleanup preparation failed", err)
		}
	}
	makeService := func() *serviceaccounts.Service {
		s, err := serviceaccounts.New(store, &journalCleanupProvider{accountProvider: newAccountProvider(), client: client})
		if err != nil {
			b.Fatal(err)
		}
		return s
	}
	service := makeService()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	cycles, complete := 0, false
	b.ReportAllocs()
	b.ResetTimer()
	for !complete {
		complete, err = service.RecoverGatewayCleanup(ctx, gateway.ID)
		if err != nil {
			b.Fatal("cleanup failed", err)
		}
		cycles++
		if cycles > 40 {
			b.Fatal("cleanup did not retain bounded progress")
		}
		if cycles <= 2 {
			mu.Lock()
			count := len(deletes)
			mu.Unlock()
			if count != cycles*100 {
				b.Fatal("cleanup repeated or lost the page across reconstruction")
			}
		}
		if cycles == 1 {
			before, err := store.LoadCheckpoint(ctx, "Gateway", gateway.ID, "gateway-account-cleanup")
			if err != nil || before.Version == 0 || before.After == "" || complete {
				b.Fatal("first cleanup page did not save progress", err)
			}
			client.Close()
			orm, err := gorm.Open(postgres.New(postgres.Config{Conn: f.db}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
			if err != nil {
				b.Fatal(err)
			}
			store, err = model.NewStore(orm)
			if err != nil {
				b.Fatal(err)
			}
			client = makeClient()
			service = makeService()
			after, err := store.LoadCheckpoint(ctx, "Gateway", gateway.ID, "gateway-account-cleanup")
			if err != nil || after != before {
				b.Fatal("reconstruction lost cleanup progress", err)
			}
		}
	}
	b.StopTimer()
	mu.Lock()
	remaining, deleted := len(clients), len(deletes)
	foreign := clients["foreign"] != nil
	for _, count := range deletes {
		if count != 1 {
			b.Error("a provider deletion was repeated")
		}
	}
	mu.Unlock()
	if remaining != 1 || !foreign || deleted != 2*size {
		b.Fatal("provider cleanup is incomplete or changed a foreign client")
	}
	var closed, audits int
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM service_accounts WHERE gateway_id=$1 AND deleted_at IS NOT NULL AND active=false", gateway.ID).Scan(&closed); err != nil || closed != size {
		b.Fatal("account closure differs", closed, err)
	}
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM service_account_audits WHERE gateway_id=$1 AND action='gateway_cleanup' AND outcome='succeeded'", gateway.ID).Scan(&audits); err != nil || audits != size {
		b.Fatal("account audit count differs", audits, err)
	}
	for _, id := range ids {
		j, err := journal(id)
		if err != nil {
			b.Fatal(err)
		}
		snapshot, err := j.Load(ctx)
		if err != nil || snapshot.Version() < 1 {
			b.Fatal("protected cleanup state is absent", err)
		}
		plain := snapshot.Reveal()
		var state struct {
			Closed bool `json:"closed"`
		}
		err = json.Unmarshal(plain, &state)
		clear(plain)
		if err != nil || !state.Closed {
			b.Fatal("protected cleanup state is not closed")
		}
	}
	membership, err := store.LoadResourceStateScope(ctx, "ServiceAccount", scope)
	if err != nil || !membership.Sealed {
		b.Fatal("cleanup did not seal journal registration", err)
	}
	b.ReportMetric(float64(size), "accounts/op")
	b.ReportMetric(float64(2*size), "journals/op")
	b.ReportMetric(float64(deleted), "provider-deletes/op")
	b.ReportMetric(float64(cycles), "cycles/op")
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &usage); err != nil {
		b.Fatal(err)
	}
	b.ReportMetric(float64(usage.Maxrss)*1024, "process-max-rss-B")
}
