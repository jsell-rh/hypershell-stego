package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGatewayDeletionRemovesServiceAccounts(t *testing.T) {
	for _, method := range []string{"REST", "gRPC"} {
		t.Run(method, func(t *testing.T) {
			f := database(t)
			provider := newAccountProvider()
			_, gateway := accountService(t, f, provider)
			key, settings := issuer(t)
			rpcSettings, _ := startAccountProvisioner(t, provider, key, settings)
			apiTLS := identity(t, "localhost")
			dir := filepath.Dir(apiTLS.config.CAFile)
			settings = append(settings, rpcSettings...)
			settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"))
			_, config := broker(t, identity(t, "localhost"))
			consumer := kafkaConsumer(t, config)
			binary := buildApplication(t)
			stop, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
			defer func() { stop() }()
			client, connection := grpcClient(t, rpcAddress, apiTLS)
			defer func() { connection.Close() }()
			root := "/api/hypershell/v1/gateways/" + gateway.ID
			owner := token(t, key, "alice")
			readEvent(t, consumer, gateway.ID)
			readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
			awaitQueueEmpty(t, f)
			for _, name := range []string{"first", "second", "third"} {
				body, _ := json.Marshal(map[string]string{"name": name})
				if code, data := requestJSON(t, "POST", address+root+"/service_accounts", owner, body); code != 201 {
					t.Fatal("account creation", code, string(data))
				}
			}
			provider.mu.Lock()
			saved := map[string]serviceaccounts.Credential{}
			for id, c := range provider.clients {
				saved[id] = c
			}
			provider.mu.Unlock()
			deletion := func(bearer string, want int) {
				t.Helper()
				if method == "REST" {
					code, data := requestJSON(t, "DELETE", address+root, bearer, nil)
					if code != want || strings.Contains(string(data), "private") {
						t.Fatal("Gateway deletion", code, string(data))
					}
					return
				}
				ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+bearer)), 12*time.Second)
				defer cancel()
				_, err := client.DeleteGateway(ctx, &pb.DeleteGatewayRequest{Id: gateway.ID})
				code := map[int]codes.Code{204: codes.OK, 404: codes.NotFound, 503: codes.Unavailable, 500: codes.Internal}[want]
				if status.Code(err) != code || err != nil && strings.Contains(err.Error(), "private") {
					t.Fatal("Gateway deletion", err)
				}
			}
			checkStored := func(live, audits int) {
				t.Helper()
				var got int
				if err := f.db.QueryRow("SELECT count(*) FROM service_accounts WHERE gateway_id=$1 AND deleted_at IS NULL", gateway.ID).Scan(&got); err != nil || got != live {
					t.Fatal("live account rows", got, err)
				}
				if err := f.db.QueryRow("SELECT count(*) FROM service_account_audits WHERE gateway_id=$1 AND action='gateway_cleanup'", gateway.ID).Scan(&got); err != nil || got != audits {
					t.Fatal("cleanup audits", got, err)
				}
			}
			deletion(token(t, key, "outsider"), 404)
			checkStored(3, 0)
			provider.mu.Lock()
			provider.failChange = true
			provider.mu.Unlock()
			deletion(owner, 503)
			checkStored(3, 0)
			if code, _ := requestJSON(t, "GET", address+root, owner, nil); code != 200 {
				t.Fatal("provider failure deleted Gateway", code)
			}
			provider.mu.Lock()
			provider.failChange = false
			provider.mu.Unlock()
			// Provider effects cannot roll back. The database must retain a safe retry
			// when the final event insert fails.
			if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_gateway_cleanup CHECK(false) NOT VALID`); err != nil {
				t.Fatal(err)
			}
			deletion(owner, 500)
			checkStored(3, 0)
			if code, _ := requestJSON(t, "GET", address+root, owner, nil); code != 200 {
				t.Fatal("event failure deleted Gateway", code)
			}
			provider.mu.Lock()
			remaining := len(provider.clients)
			provider.mu.Unlock()
			if remaining != 0 {
				t.Fatal("provider cleanup did not run", remaining)
			}
			if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_gateway_cleanup`); err != nil {
				t.Fatal(err)
			}
			connection.Close()
			stop()
			stop, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
			client, connection = grpcClient(t, rpcAddress, apiTLS)
			deletion(owner, 204)
			checkStored(0, 3)
			readGatewayEvent(t, consumer, gateway.ID, "Delete", "gateway.deleted")
			awaitQueueEmpty(t, f)
			if code, _ := requestJSON(t, "GET", address+root+"/service_accounts", owner, nil); code != 404 {
				t.Fatal("deleted accounts are visible", code)
			}
			deletion(owner, 404)
			// A late external create after loss of the database lock is still removed
			// through the retained account tombstone after another API restart.
			connection.Close()
			stop()
			provider.mu.Lock()
			for id, c := range saved {
				provider.clients[id] = c
			}
			provider.mu.Unlock()
			stop, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
			client, connection = grpcClient(t, rpcAddress, apiTLS)
			deadline := time.Now().Add(15 * time.Second)
			for {
				provider.mu.Lock()
				remaining = len(provider.clients)
				provider.mu.Unlock()
				if remaining == 0 {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("restart left late provider clients", remaining)
				}
				time.Sleep(50 * time.Millisecond)
			}
			checkStored(0, 3)
		})
	}
}

func TestGatewayAccountCleanupSerializesCreation(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	accounts, gateway := accountService(t, f, provider)
	service, err := gateways.New(f.storage, gateways.Options{DatabaseProvider: gateways.ProviderCNPG, AccountCleaner: accounts})
	if err != nil {
		t.Fatal(err)
	}
	provider.entered = make(chan struct{})
	provider.release = make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	created := make(chan error, 1)
	deleted := make(chan error, 1)
	go func() {
		_, err := accounts.Create(ctx, principal("alice"), gateway.ID, accountInput("concurrent"))
		created <- err
	}()
	select {
	case <-provider.entered:
	case <-ctx.Done():
		t.Fatal("provider did not start")
	}
	go func() { deleted <- service.Delete(ctx, principal("alice"), gateway.ID) }()
	close(provider.release)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, principal("alice"), gateway.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("Gateway survived completed cleanup", err)
	}
	provider.mu.Lock()
	remaining := len(provider.clients)
	calls := provider.calls
	provider.entered = nil
	provider.release = nil
	provider.mu.Unlock()
	if remaining != 0 {
		t.Fatal("concurrent create escaped cleanup")
	}
	if _, err := accounts.Create(ctx, principal("alice"), gateway.ID, accountInput("after-deletion")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("created account after Gateway deletion", err)
	}
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if provider.calls != calls {
		t.Fatal("deleted Gateway reached provider creation")
	}
}
