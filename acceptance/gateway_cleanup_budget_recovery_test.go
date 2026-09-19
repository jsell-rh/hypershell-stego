package acceptance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type budgetCleanupProvider struct{ *boundedCleanupProvider }

func (p *budgetCleanupProvider) Delete(ctx context.Context, gatewayID, id, uuid string) error {
	timer := time.NewTimer(450 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return p.boundedCleanupProvider.Delete(ctx, gatewayID, id, uuid)
	}
}

// Six bounded actions require more than one work pass. A planned pause must
// preserve clean progress, so process replacement can finish the same cycle.
func TestGatewayAccountCleanupBudgetResumesAfterRestart(t *testing.T) {
	f := database(t)
	original := newAccountProvider()
	accounts, gateway := accountService(t, f, original)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ids := []string{}
	for n := 0; n < 6; n++ {
		created, err := accounts.Create(ctx, principal("alice"), gateway.ID, accountInput(fmt.Sprintf("budget-%d", n)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, created.Account.ID)
	}
	if err := f.storage.Delete(ctx, "Gateway", gateway.ID); err != nil {
		t.Fatal(err)
	}
	provider := &budgetCleanupProvider{&boundedCleanupProvider{accountProvider: original, confirmed: map[string]int{}}}
	accounts, err := serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := accounts.RecoverGatewayCleanup(ctx, gateway.ID)
	if err != nil || complete || len(provider.confirmed) == 0 || len(provider.confirmed) >= len(ids) {
		t.Fatal("work budget did not save a clean partial pass", complete, len(provider.confirmed), err)
	}
	checkpoint, err := f.storage.LoadCheckpoint(ctx, "Gateway", gateway.ID, "gateway-account-cleanup")
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := runtime.DecodeCycle(checkpoint.After)
	if err != nil || checkpoint.Version < 1 || cycle.Complete || cycle.Failed || cycle.After == "" {
		t.Fatal("planned pause saved a failed or complete cycle", checkpoint, cycle, err)
	}
	scope := gateways.AccountProviderStateScope(gateway.ID)
	membership, err := f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scope)
	if err != nil || membership.Sealed || provider.inventory != 0 {
		t.Fatal("partial work claimed cleanup", membership, provider.inventory, err)
	}
	firstCount := len(provider.confirmed)
	for pass := 0; pass < 6 && !complete; pass++ {
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
		if err != nil {
			t.Fatal("resumed clean cycle failed", err)
		}
	}
	if !complete || provider.inventory != 1 {
		t.Fatal("resumed cycle did not reach inventory", complete, provider.inventory)
	}
	for _, id := range ids {
		if provider.confirmed[id] != 1 {
			t.Fatal("recovery repeated or omitted an account", provider.confirmed[id])
		}
	}
	membership, err = f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scope)
	if err != nil || !membership.Sealed {
		t.Fatal("completed cycle did not seal the scope", membership, err)
	}
	var closed, audits int
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM service_accounts WHERE gateway_id=$1 AND deleted_at IS NOT NULL AND active=false", gateway.ID).Scan(&closed); err != nil || closed != len(ids) {
		t.Fatal("account metadata did not close", closed, err)
	}
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM service_account_audits WHERE gateway_id=$1 AND action='gateway_cleanup' AND outcome='succeeded'", gateway.ID).Scan(&audits); err != nil || audits != len(ids) {
		t.Fatal("cleanup audit was lost or repeated", audits, err)
	}
	t.Logf("Saved %d completed accounts before restart; all six closed once and inventory sealed the scope", firstCount)
}
