package acceptance

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type boundedCleanupProvider struct {
	*accountProvider
	calls     int
	stall     bool
	confirmed map[string]int
	inventory int
}

func (p *boundedCleanupProvider) DeleteGateway(context.Context, string) error {
	p.inventory++
	return nil
}
func (p *boundedCleanupProvider) Delete(ctx context.Context, gatewayID, id, uuid string) error {
	p.calls++
	if p.stall && p.calls == 2 {
		<-ctx.Done()
		return ctx.Err()
	}
	if err := p.accountProvider.Delete(ctx, gatewayID, id, uuid); err != nil {
		return err
	}
	p.confirmed[id]++
	return nil
}

func TestGatewayAccountCleanupRecoveryReachesTailAfterRestart(t *testing.T) {
	f := database(t)
	provider := &boundedCleanupProvider{accountProvider: newAccountProvider(), confirmed: map[string]int{}}
	_, gateway := accountService(t, f, provider.accountProvider)
	accounts, err := serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	live, err := accounts.Create(context.Background(), principal("alice"), gateway.ID, accountInput("cleanup-live"))
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{live.Account.ID}
	for i := 0; i < 2; i++ {
		id := ksuid.New().String()
		row := model.ServiceAccount{Meta: model.Meta{ID: id}, GatewayID: gateway.ID, Name: fmt.Sprintf("retained-%d", i), CredentialType: "client_secret", Role: serviceaccounts.RoleUser, Status: "error", CreatedByUserID: live.Account.CreatedByUserID, ClientID: "hs-sa-" + gateway.ID + "-" + id, ExpiresAt: time.Now().Add(time.Hour)}
		if err := f.storage.Create(context.Background(), "ServiceAccount", row); err != nil {
			t.Fatal(err)
		}
		if err := f.storage.Delete(context.Background(), "ServiceAccount", id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	if complete, err := accounts.RecoverGatewayCleanup(context.Background(), gateway.ID); !errors.Is(err, runtime.ErrScanContract) || complete || provider.calls != 0 {
		t.Fatal("live Gateway cleanup was permitted", complete, err)
	}
	// This fixture starts at a committed deletion. The REST acceptance gate must
	// separately prove that the authorized HTTP request creates this state.
	if err := f.storage.Delete(context.Background(), "Gateway", gateway.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.Create(context.Background(), principal("alice"), gateway.ID, accountInput("blocked")); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("deleted Gateway accepted an account", err)
	}
	provider.stall = true
	for attempt := 0; attempt < 1; attempt++ {
		orm, err := gorm.Open(postgres.New(postgres.Config{Conn: f.db}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		restarted, err := model.NewStore(orm)
		if err != nil {
			t.Fatal(err)
		}
		accounts, err = serviceaccounts.New(restarted, provider)
		if err != nil {
			t.Fatal(err)
		}
		provider.calls = 0
		complete, err := accounts.RecoverGatewayCleanup(context.Background(), gateway.ID)
		if complete || (!errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, runtime.ErrCycleFailed)) {
			t.Fatal("interrupted cycle claimed completion", complete, err)
		}
	}
	if len(provider.confirmed) != len(ids)-1 {
		t.Fatalf("a failed account stopped independent cleanup: reached %d of %d IDs", len(provider.confirmed), len(ids))
	}
	if provider.inventory != 0 {
		t.Fatal("failed retained cycle ran inventory completion")
	}
	provider.stall = false
	accounts, err = serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	if complete, err := accounts.RecoverGatewayCleanup(context.Background(), gateway.ID); err != nil || !complete {
		t.Fatal("successful cleanup did not complete", complete, err)
	}
	if complete, err := accounts.RecoverGatewayCleanup(context.Background(), gateway.ID); err != nil || !complete {
		t.Fatal("repeated cleanup", complete, err)
	}
	if len(provider.confirmed) != len(ids) {
		t.Fatalf("restarted cleanup reached %d of %d retained IDs", len(provider.confirmed), len(ids))
	}
	if provider.inventory != 2 {
		t.Fatal("completion omitted repeated provider inventory")
	}
	var audits int
	if err := f.db.QueryRow("SELECT count(*) FROM service_account_audits WHERE gateway_id=$1 AND action='gateway_cleanup' AND outcome='succeeded'", gateway.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("cleanup audit was lost or duplicated", audits, err)
	}
	t.Logf("Restarted cleanup reached all %d retained IDs; the live account has one cleanup audit", len(ids))
}

// The fixture has one more row than a scan page. This checks the page boundary;
// it does not measure capacity or load the workstation.
func TestGatewayAccountCleanupRecoveryKeepsPageCheckpoint(t *testing.T) {
	f := database(t)
	original := newAccountProvider()
	accounts, gateway := accountService(t, f, original)
	live, err := accounts.Create(context.Background(), principal("alice"), gateway.ID, accountInput("page-source"))
	if err != nil {
		t.Fatal(err)
	}
	ids := []string{live.Account.ID}
	for i := 0; i < 100; i++ {
		id := ksuid.New().String()
		row := model.ServiceAccount{Meta: model.Meta{ID: id}, GatewayID: gateway.ID, Name: fmt.Sprintf("retained-%d", i), CredentialType: "client_secret", Role: serviceaccounts.RoleUser, Status: "error", CreatedByUserID: live.Account.CreatedByUserID, ClientID: "hs-sa-" + gateway.ID + "-" + id, ExpiresAt: time.Now().Add(time.Hour)}
		if err := f.storage.Create(context.Background(), "ServiceAccount", row); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	for _, id := range ids {
		if err := f.storage.Delete(context.Background(), "ServiceAccount", id); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.storage.Delete(context.Background(), "Gateway", gateway.ID); err != nil {
		t.Fatal(err)
	}
	provider := &boundedCleanupProvider{accountProvider: original, confirmed: map[string]int{}}
	accounts, err = serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := accounts.RecoverGatewayCleanup(context.Background(), gateway.ID)
	if err != nil || complete || len(provider.confirmed) != 100 || provider.inventory != 0 {
		t.Fatal("first bounded page", complete, len(provider.confirmed), err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: f.db}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := model.NewStore(orm)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err = serviceaccounts.New(restarted, provider)
	if err != nil {
		t.Fatal(err)
	}
	complete, err = accounts.RecoverGatewayCleanup(context.Background(), gateway.ID)
	if err != nil || !complete || len(provider.confirmed) != len(ids) || provider.inventory != 1 {
		t.Fatal("resumed page", complete, len(provider.confirmed), err)
	}
	for id, count := range provider.confirmed {
		if count != 1 {
			t.Fatal("completed prefix repeated after restart", id, count)
		}
	}
	t.Log("The first pass saved 100 retained IDs; a reconstructed service completed the last ID")
}

func TestGatewayJournalCleanupRecoveryKeepsPageCheckpoint(t *testing.T) {
	f := database(t)
	original := newAccountProvider()
	_, gateway := accountService(t, f, original)
	ctx := context.Background()
	ids := make([]string, 0, 101)
	// The provider is a recording fixture. Empty state records are sufficient to
	// check that the application visits all retained keys without domain rows.
	for i := 0; i < 101; i++ {
		id := ksuid.New().String()
		ids = append(ids, id)
		err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
			_, err := tx.(storage.ResourceStateStore).SaveResourceState(ctx, "ServiceAccount", id, gateways.AccountProviderStateScope(gateway.ID), 0, nil)
			return err
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := f.storage.Delete(ctx, "Gateway", gateway.ID); err != nil {
		t.Fatal(err)
	}
	provider := &boundedCleanupProvider{accountProvider: original, confirmed: map[string]int{}}
	accounts, err := serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := accounts.RecoverGatewayCleanup(ctx, gateway.ID)
	if err != nil || complete || len(provider.confirmed) != 100 || provider.inventory != 0 {
		t.Fatal("first journal page differs", complete, len(provider.confirmed), err)
	}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: f.db}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	restarted, err := model.NewStore(orm)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err = serviceaccounts.New(restarted, provider)
	if err != nil {
		t.Fatal(err)
	}
	complete, err = accounts.RecoverGatewayCleanup(ctx, gateway.ID)
	if err != nil || !complete || len(provider.confirmed) != len(ids) || provider.inventory != 1 {
		t.Fatal("resumed journal page differs", complete, len(provider.confirmed), err)
	}
	for id, count := range provider.confirmed {
		if count != 1 {
			t.Fatal("journal prefix repeated after reconstruction", id, count)
		}
	}
}
