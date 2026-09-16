package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func requireDeletingGateway(t *testing.T, address, bearer string) {
	t.Helper()
	code, data := requestJSON(t, "GET", address, bearer, nil)
	var row httpapi.Gateway
	if code != 200 || json.Unmarshal(data, &row) != nil || row.Phase == nil || *row.Phase != "Deleting" {
		t.Fatal("pending Gateway is not visible as Deleting", code)
	}
}

func TestGatewayDurableDeletionThroughGeneratedTransports(t *testing.T) {
	for _, method := range []string{"REST", "gRPC"} {
		t.Run(method, func(t *testing.T) {
			f := database(t)
			provider := newAccountProvider()
			accounts, gateway := accountService(t, f, provider)
			created, err := accounts.Create(context.Background(), principal("alice"), gateway.ID, accountInput("asynchronous"))
			if err != nil {
				t.Fatal(err)
			}
			provider.mu.Lock()
			provider.failChange = true
			provider.mu.Unlock()
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
			deletion := func(bearer string, want int) {
				t.Helper()
				if method == "REST" {
					code, body := requestJSON(t, "DELETE", address+root, bearer, nil)
					if code != want || code == 202 && len(body) != 0 {
						t.Fatal("deletion response", code, string(body))
					}
					return
				}
				ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+bearer)), 12*time.Second)
				defer cancel()
				_, err := client.DeleteGateway(ctx, &pb.DeleteGatewayRequest{Id: gateway.ID})
				expected := map[int]codes.Code{202: codes.OK, 404: codes.NotFound, 500: codes.Internal}[want]
				if status.Code(err) != expected {
					t.Fatal("gRPC deletion", err)
				}
			}
			deletion(token(t, key, "outsider"), 404)
			if _, err := f.storage.Get(context.Background(), "Gateway", gateway.ID); err != nil {
				t.Fatal("denied deletion changed Gateway", err)
			}
			// A failed request event must leave the Gateway live.
			if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_request CHECK (kind <> 'gateway.updated') NOT VALID"); err != nil {
				t.Fatal(err)
			}
			deletion(owner, 500)
			if _, err := f.storage.Get(context.Background(), "Gateway", gateway.ID); err != nil {
				t.Fatal("failed request committed deletion", err)
			}
			if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_request"); err != nil {
				t.Fatal(err)
			}
			deletion(owner, 202)
			deletion(owner, 202)
			checkPending := func() {
				t.Helper()
				code, body := requestJSON(t, "GET", address+root, owner, nil)
				var row httpapi.Gateway
				if code != 200 || json.Unmarshal(body, &row) != nil || row.Phase == nil || *row.Phase != "Deleting" {
					t.Fatal("pending Gateway", code, string(body))
				}
				if code, _ := requestJSON(t, "GET", address+root, token(t, key, "outsider"), nil); code != 404 {
					t.Fatal("pending Gateway escaped access", code)
				}
				rpcContext, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+owner)), 3*time.Second)
				rpcRow, rpcErr := client.GetGateway(rpcContext, &pb.GetGatewayRequest{Id: gateway.ID})
				cancel()
				if rpcErr != nil || rpcRow == nil || rpcRow.GetGateway().GetPhase() != "Deleting" {
					t.Fatal("gRPC pending Gateway", rpcErr)
				}

				for _, user := range []string{"alice", "outsider"} {
					code, body := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways", token(t, key, user), nil)
					var page struct {
						Total int               `json:"total"`
						Items []httpapi.Gateway `json:"items"`
					}
					if code != 200 || json.Unmarshal(body, &page) != nil {
						t.Fatal("pending list", code, string(body))
					}
					want := 0
					if user == "alice" {
						want = 1
					}
					if page.Total != want || len(page.Items) != want {
						t.Fatal("pending list escaped access", user, page.Total, len(page.Items))
					}
				}
			}
			checkPending()
			if code, _ := requestJSON(t, "POST", address+root+"/service_accounts", owner, []byte(`{"name":"blocked"}`)); code != 404 {
				t.Fatal("deleting Gateway accepted new account", code)
			}
			if code, _ := requestJSON(t, "PATCH", address+root, owner, []byte(`{"name":"changed"}`)); code != 404 {
				t.Fatal("deleting Gateway accepted mutation", code)
			}
			// Other controller owners confirm their application work. Account cleanup is
			// still blocked. These observations cannot remove the pending Gateway.
			for _, name := range []string{"identity", "workload", "sql"} {
				err := f.storage.WithTransaction(context.Background(), func(ctx context.Context, tx storage.Transaction) error {
					value, err := tx.(storage.RetainedReader).GetRetained(ctx, "Gateway", gateway.ID)
					if err != nil {
						return err
					}
					row := value.(model.Gateway)
					target := ""
					if name != "identity" {
						target = gateway.ClusterID
					}
					return gateways.RecordCleanup(ctx, tx, row.ID, row.ResourceVersion, name, target, true)
				})
				if err != nil {
					t.Fatal("controller cleanup observation", name, err)
				}
			}
			checkPending()
			// Account metadata may commit before the final event. Event failure must keep
			// the Gateway visible and retain a safe retry across process replacement.
			if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_final CHECK (kind <> 'gateway.deleted') NOT VALID"); err != nil {
				t.Fatal(err)
			}
			provider.mu.Lock()
			provider.failChange = false
			provider.mu.Unlock()
			deadline := time.Now().Add(20 * time.Second)
			for {
				var deleted bool
				if err := f.db.QueryRow("SELECT deleted_at IS NOT NULL FROM service_accounts WHERE id=$1", created.Account.ID).Scan(&deleted); err != nil {
					t.Fatal(err)
				}
				if deleted {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("account controller did not run")
				}
				time.Sleep(50 * time.Millisecond)
			}
			checkPending()
			connection.Close()
			stop()
			if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_final"); err != nil {
				t.Fatal(err)
			}
			stop, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
			client, connection = grpcClient(t, rpcAddress, apiTLS)
			deadline = time.Now().Add(20 * time.Second)
			for {
				code, _ := requestJSON(t, "GET", address+root, owner, nil)
				if code == 404 {
					break
				}
				if code != 200 || time.Now().After(deadline) {
					t.Fatal("restart did not finalize Gateway", code)
				}
				time.Sleep(50 * time.Millisecond)
			}
			deletion(owner, 404)
			var audits int
			if err := f.db.QueryRow("SELECT count(*) FROM service_account_audits WHERE service_account_id=$1 AND action='gateway_cleanup'", created.Account.ID).Scan(&audits); err != nil || audits != 1 {
				t.Fatal("cleanup audit was lost or duplicated", audits, err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			delivered := false
			for !delivered && ctx.Err() == nil {
				for _, record := range consumer.PollRecords(ctx, 1).Records() {
					if string(record.Key) != gateway.ID {
						continue
					}
					var payload map[string]string
					if json.Unmarshal(record.Value, &payload) != nil {
						t.Fatal("invalid event")
					}
					if payload["event_type"] != "Delete" {
						continue
					}
					if payload["source"] != "Gateways" || payload["source_id"] != gateway.ID {
						t.Fatal("wrong final event")
					}
					for _, header := range record.Headers {
						if header.Key == "stego-message-kind" && string(header.Value) == "gateway.deleted" {
							delivered = true
						}
					}
				}
			}
			if !delivered {
				t.Fatal("generated runtime did not deliver final deletion")
			}
			value, err := f.storage.GetRetained(context.Background(), "Gateway", gateway.ID)
			if err != nil {
				t.Fatal(err)
			}
			row := value.(model.Gateway)
			if row.DeletionFinalizedAt == nil {
				t.Fatal("finalization marker is absent")
			}
			if _, err := f.service.Get(context.Background(), principal("alice"), gateway.ID); !errors.Is(err, storage.ErrNotFound) {
				t.Fatal("finalized Gateway remained visible", err)
			}
		})
	}
}

func TestGatewayCleanupObservationDoesNotRepeatEvents(t *testing.T) {
	f := database(t)
	gateway, err := f.service.Create(context.Background(), principal("alice", "gateway:creator"), f.request("cleanup-events"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Delete(context.Background(), principal("alice"), gateway.ID); err != nil {
		t.Fatal(err)
	}
	observe := func(owner string, complete bool) {
		t.Helper()
		err := f.storage.WithTransaction(context.Background(), func(ctx context.Context, tx storage.Transaction) error {
			value, err := tx.(storage.RetainedReader).GetRetained(ctx, "Gateway", gateway.ID)
			if err != nil {
				return err
			}
			row := value.(model.Gateway)
			target := ""
			if owner == "workload" || owner == "sql" {
				target = gateway.ClusterID
			}
			return gateways.RecordCleanup(ctx, tx, row.ID, row.ResourceVersion, owner, target, complete)
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	before := count(t, f.db, "stego_outbox.messages")
	observe("accounts", false)
	if count(t, f.db, "stego_outbox.messages") != before {
		t.Fatal("unchanged pending cleanup published an event")
	}
	for _, owner := range []string{"accounts", "identity", "workload", "sql"} {
		observe(owner, true)
	}
	after := count(t, f.db, "stego_outbox.messages")
	for _, owner := range []string{"accounts", "identity", "workload", "sql"} {
		observe(owner, true)
	}
	if count(t, f.db, "stego_outbox.messages") != after {
		t.Fatal("unchanged completed cleanup published an event")
	}
	observe("accounts", false)
	if _, err := f.service.Get(context.Background(), principal("alice"), gateway.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("late cleanup restored a finalized Gateway", err)
	}
	var final int
	if err := f.db.QueryRow("SELECT count(*) FROM stego_outbox.messages WHERE resource_key=$1 AND kind='gateway.deleted'", gateway.ID).Scan(&final); err != nil || final != 1 {
		t.Fatal("final deletion notice repeated", final, err)
	}
}

func TestGatewayDeletingPhaseMatchesSearch(t *testing.T) {
	f := database(t)
	row, err := f.service.Create(context.Background(), principal("alice", "gateway:creator"), f.request("phase-search"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Delete(context.Background(), principal("alice"), row.ID); err != nil {
		t.Fatal(err)
	}
	row, err = f.service.Get(context.Background(), principal("alice"), row.ID)
	if err != nil {
		t.Fatal(err)
	}
	row = row.CurrentObservations()
	if row.Phase == nil || *row.Phase != "Deleting" {
		t.Fatal("pending display phase differs")
	}
	for _, user := range []string{"alice", "outsider"} {
		result, err := f.service.Search(context.Background(), principal(user), 1, 1, "phase = 'Deleting'", []storage.OrderByField{{Field: "phase", Direction: "asc"}})
		want := int64(0)
		if user == "alice" {
			want = 1
		}
		if err != nil || result.Total != want || len(result.Items.([]model.Gateway)) != int(want) {
			t.Fatal("phase search and access differ from public state", user, result.Total, err)
		}
	}
}
