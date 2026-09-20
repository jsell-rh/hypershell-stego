package acceptance

import (
	"context"
	"errors"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	"github.com/segmentio/ksuid"
)

type journalInventoryProvider struct {
	*boundedCleanupProvider
	inventoryHook func(context.Context, string) error
}

func (p *journalInventoryProvider) DeleteGateway(ctx context.Context, id string) error {
	p.inventory++
	if p.inventoryHook != nil {
		return p.inventoryHook(ctx, id)
	}
	return nil
}
func saveJournalKey(t *testing.T, f *fixture, gatewayID, id string) error {
	t.Helper()
	return f.storage.WithTransaction(context.Background(), func(ctx context.Context, tx storage.Transaction) error {
		_, err := tx.(storage.ResourceStateStore).SaveResourceState(ctx, "ServiceAccount", id, gateways.AccountProviderStateScope(gatewayID), 0, nil)
		return err
	})
}

func TestGatewayJournalAddedAfterLastPagePreventsCompletion(t *testing.T) {
	f := database(t)
	original := newAccountProvider()
	_, gateway := accountService(t, f, original)
	ctx := context.Background()
	if err := f.service.Delete(ctx, principal("alice"), gateway.ID); err != nil {
		t.Fatal(err)
	}
	id := ksuid.New().String()
	provider := &journalInventoryProvider{boundedCleanupProvider: &boundedCleanupProvider{accountProvider: original, confirmed: map[string]int{}}}
	provider.inventoryHook = func(context.Context, string) error {
		if provider.inventory == 1 {
			return saveJournalKey(t, f, gateway.ID, id)
		}
		return nil
	}
	accounts, err := serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	complete, err := accounts.RecoverGatewayCleanup(ctx, gateway.ID)
	if complete || !errors.Is(err, storage.ErrResourceStateConflict) {
		t.Fatal("key added after the last page permitted completion", complete, err)
	}
	scope, err := f.storage.LoadResourceStateScope(ctx, "ServiceAccount", gateways.AccountProviderStateScope(gateway.ID))
	if err != nil || scope.Sealed {
		t.Fatal("failed cleanup sealed registration", err)
	}
	if complete, err := accounts.RecoverGatewayCleanup(ctx, gateway.ID); err != nil || !complete {
		t.Fatal("new key did not recover", complete, err)
	}
	if provider.confirmed[id] != 1 {
		t.Fatal("the late journal was not visited")
	}
	scope, err = f.storage.LoadResourceStateScope(ctx, "ServiceAccount", gateways.AccountProviderStateScope(gateway.ID))
	if err != nil || !scope.Sealed {
		t.Fatal("complete cleanup did not seal registration", err)
	}
	if err := saveJournalKey(t, f, gateway.ID, ksuid.New().String()); !errors.Is(err, storage.ErrResourceStateConflict) {
		t.Fatal("closed cleanup accepted a new obligation", err)
	}
}

func TestGatewayJournalClosureRollsBackWithFinalEvent(t *testing.T) {
	f := database(t)
	original := newAccountProvider()
	accounts, gateway := accountService(t, f, original)
	ctx := context.Background()
	if err := f.service.Delete(ctx, principal("alice"), gateway.ID); err != nil {
		t.Fatal(err)
	}
	completeFakeGatewayOwners(t, f, gateway.ID, "identity", "workload", "sql", "allocation")
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_journal_final CHECK(kind <> 'gateway.deleted') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if complete, err := accounts.RecoverGatewayCleanup(ctx, gateway.ID); complete || err == nil {
		t.Fatal("failed final event permitted cleanup", complete, err)
	}
	scopeName := gateways.AccountProviderStateScope(gateway.ID)
	scope, err := f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scopeName)
	if err != nil || scope.Sealed || scope.Revision != 0 {
		t.Fatal("failed event retained scope closure", scope, err)
	}
	row, err := f.service.Get(ctx, principal("alice"), gateway.ID)
	if err != nil || !row.DeletedAt.Valid || row.DeletionFinalizedAt != nil || row.CleanupComplete("accounts") {
		t.Fatal("failed event retained cleanup completion", err)
	}
	// A failed final commit must leave registration open. The next full cycle must
	// include this new key before it can close registration and finish deletion.
	if err := saveJournalKey(t, f, gateway.ID, ksuid.New().String()); err != nil {
		t.Fatal("rollback blocked a new key", err)
	}
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_journal_final"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if complete, err := accounts.RecoverGatewayCleanup(ctx, gateway.ID); err != nil || !complete {
			t.Fatal("final cleanup retry failed", complete, err)
		}
	}
	scope, err = f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scopeName)
	if err != nil || !scope.Sealed {
		t.Fatal("final commit lost scope closure", err)
	}
	if _, err := f.service.Get(ctx, principal("alice"), gateway.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("finalized Gateway remained visible", err)
	}
	var events int
	if err := f.db.QueryRow("SELECT count(*) FROM stego_outbox.messages WHERE resource_key=$1 AND kind='gateway.deleted'", gateway.ID).Scan(&events); err != nil || events != 1 {
		t.Fatal("final event differs", events, err)
	}
}
