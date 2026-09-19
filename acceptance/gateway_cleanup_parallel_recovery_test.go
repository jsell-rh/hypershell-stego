package acceptance

import (
	"context"
	"errors"
	"fmt"
	"sync"
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

// The provider records account rows and retained journal visits. Delay outside
// the mutex permits independent keys to run at the same time.
type parallelCleanupProvider struct {
	*accountProvider
	lock       sync.Mutex
	active     map[string]bool
	rows       map[string]int
	journals   map[string]int
	peak       int
	inventory  int
	violations int
}

func (p *parallelCleanupProvider) Delete(ctx context.Context, gatewayID, id, uuid string) error {
	p.lock.Lock()
	if p.active[id] || uuid == "" && p.rows[id] == 0 {
		p.violations++
		p.lock.Unlock()
		return errors.New("account and journal work overlapped or changed order")
	}
	p.active[id] = true
	p.peak = max(p.peak, len(p.active))
	p.lock.Unlock()
	defer func() {
		p.lock.Lock()
		delete(p.active, id)
		p.lock.Unlock()
	}()
	timer := time.NewTimer(450 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
	}
	if err := p.accountProvider.Delete(ctx, gatewayID, id, uuid); err != nil {
		return err
	}
	p.lock.Lock()
	defer p.lock.Unlock()
	if uuid == "" {
		p.journals[id]++
	} else {
		p.rows[id]++
	}
	return nil
}

func (p *parallelCleanupProvider) InventoryPage(context.Context, string, string, string, int) (runtime.CursorPage[string], error) {
	p.lock.Lock()
	defer p.lock.Unlock()
	p.inventory++
	if len(p.active) != 0 || len(p.rows) != 32 || len(p.journals) != 32 {
		return runtime.CursorPage[string]{}, errors.New("inventory started before account work finished")
	}
	return runtime.CursorPage[string]{}, nil
}

func TestGatewayAccountCleanupParallelResumesAfterRestart(t *testing.T) {
	f := database(t)
	original := newAccountProvider()
	_, gateway := accountService(t, f, original)
	options := serviceaccounts.DefaultOptions()
	options.Limits = serviceaccounts.Limits{PerGateway: 100, PerCreator: 100}
	accounts, err := serviceaccounts.NewWithOptions(f.storage, original, options)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ids := []string{}
	for n := 0; n < 32; n++ {
		created, err := accounts.Create(ctx, principal("alice"), gateway.ID, accountInput(fmt.Sprintf("parallel-%d", n)))
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, created.Account.ID)
		// Empty records select the journal path. The real provider capacity test
		// checks encrypted journal contents and remote closure separately.
		if err := saveJournalKey(t, f, gateway.ID, created.Account.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.storage.Delete(ctx, "Gateway", gateway.ID); err != nil {
		t.Fatal(err)
	}
	provider := &parallelCleanupProvider{accountProvider: original, active: map[string]bool{}, rows: map[string]int{}, journals: map[string]int{}}
	accounts, err = serviceaccounts.NewWithOptions(f.storage, provider, options)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := accounts.RecoverGatewayCleanup(ctx, gateway.ID)
	if err != nil || complete || len(provider.rows) == 0 || len(provider.rows) >= len(ids) || len(provider.active) != 0 {
		t.Fatal("parallel work did not save a partial pass and join its callbacks", complete, len(provider.rows), err)
	}
	firstCount := len(provider.rows)
	checkpoint, err := f.storage.LoadCheckpoint(ctx, "Gateway", gateway.ID, "gateway-account-cleanup")
	if err != nil {
		t.Fatal(err)
	}
	cycle, err := runtime.DecodeCycle(checkpoint.After)
	if err != nil || checkpoint.Version < 1 || cycle.Complete || cycle.Failed || cycle.After == "" {
		t.Fatal("parallel pause lost clean saved progress", checkpoint, cycle, err)
	}
	scope := gateways.AccountProviderStateScope(gateway.ID)
	membership, err := f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scope)
	if err != nil || membership.Sealed || provider.inventory != 0 {
		t.Fatal("partial work claimed cleanup", membership, provider.inventory, err)
	}
	for pass := 0; pass < 12 && !complete; pass++ {
		orm, err := gorm.Open(postgres.New(postgres.Config{Conn: f.db}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
		if err != nil {
			t.Fatal(err)
		}
		restarted, err := model.NewStore(orm)
		if err != nil {
			t.Fatal(err)
		}
		accounts, err = serviceaccounts.NewWithOptions(restarted, provider, options)
		if err != nil {
			t.Fatal(err)
		}
		complete, err = accounts.RecoverGatewayCleanup(ctx, gateway.ID)
		if err != nil || len(provider.active) != 0 {
			t.Fatal("resumed parallel cycle failed or left callbacks running", err)
		}
	}
	if !complete || provider.inventory != 1 || provider.violations != 0 || provider.peak < 2 || provider.peak > options.CleanupWorkers {
		t.Fatal("parallel cleanup did not complete with bounded independent work", complete, provider.inventory, provider.violations, provider.peak)
	}
	for _, id := range ids {
		// Effects after a saved prefix can repeat. Both forms must be visited,
		// and the SQL transaction must still record exactly one success audit.
		if provider.rows[id] < 1 || provider.journals[id] < 1 {
			t.Fatal("recovery omitted an account row or journal")
		}
	}
	membership, err = f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scope)
	if err != nil || !membership.Sealed || membership.Revision != int64(len(ids)) {
		t.Fatal("complete cleanup did not seal all journal registrations", membership, err)
	}
	original.mu.Lock()
	remaining := len(original.clients)
	original.mu.Unlock()
	if remaining != 0 {
		t.Fatal("completed cleanup retained provider clients", remaining)
	}
	var closed int
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM service_accounts WHERE gateway_id=$1 AND deleted_at IS NOT NULL AND active=false", gateway.ID).Scan(&closed); err != nil || closed != len(ids) {
		t.Fatal("account metadata did not close", closed, err)
	}
	rows, err := f.db.QueryContext(ctx, "SELECT service_account_id, count(*) FROM service_account_audits WHERE gateway_id=$1 AND action='gateway_cleanup' AND outcome='succeeded' GROUP BY service_account_id", gateway.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	audits := map[string]int{}
	for rows.Next() {
		var id string
		var count int
		if err := rows.Scan(&id, &count); err != nil {
			t.Fatal(err)
		}
		audits[id] = count
	}
	if err := rows.Err(); err != nil || len(audits) != len(ids) {
		t.Fatal("cleanup audit identities differ", len(audits), err)
	}
	for _, id := range ids {
		if audits[id] != 1 {
			t.Fatal("cleanup audit was lost or repeated for an account", audits[id])
		}
	}
	t.Logf("Saved %d account rows before reconstruction; peak %d callbacks; all 32 rows and journals visited; 32 success audits", firstCount, provider.peak)
}
