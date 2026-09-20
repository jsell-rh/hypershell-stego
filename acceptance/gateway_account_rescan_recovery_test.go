package acceptance

import (
	"context"
	"errors"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

// A clean verification page is not evidence that previously removed accounts
// returned. This fixture crosses one page boundary without new provider effects.
func TestGatewayAccountCleanupRecoveryRetainsProofDuringCleanRescan(t *testing.T) {
	testGatewayAccountCleanupRescanProof(t, "clean")
}

func TestGatewayAccountCleanupRecoveryReopensProofOnProviderFailure(t *testing.T) {
	testGatewayAccountCleanupRescanProof(t, "failure")
}

func TestGatewayAccountCleanupRecoveryInvalidatesProofWhenInputsChange(t *testing.T) {
	testGatewayAccountCleanupRescanProof(t, "changed")
}

type rescanCleanupProvider struct {
	*boundedCleanupProvider
	failure error
}

func (p *rescanCleanupProvider) Delete(ctx context.Context, gatewayID, id, uuid string) error {
	if p.failure != nil {
		p.calls++
		return p.failure
	}
	return p.boundedCleanupProvider.Delete(ctx, gatewayID, id, uuid)
}

func testGatewayAccountCleanupRescanProof(t *testing.T, mode string) {
	t.Helper()
	f := database(t)
	ctx := context.Background()
	original := newAccountProvider()
	_, gateway := accountService(t, f, original)
	scope := gateways.AccountProviderStateScope(gateway.ID)
	for i := 0; i < 101; i++ {
		id := ksuid.New().String()
		if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
			_, err := tx.(storage.ResourceStateStore).SaveResourceState(ctx, "ServiceAccount", id, scope, 0, nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.storage.Delete(ctx, "Gateway", gateway.ID); err != nil {
		t.Fatal(err)
	}
	provider := &rescanCleanupProvider{boundedCleanupProvider: &boundedCleanupProvider{accountProvider: original, confirmed: map[string]int{}}}
	accounts, err := serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 2; pass++ {
		complete, err := accounts.RecoverGatewayCleanup(ctx, gateway.ID)
		if err != nil || complete != (pass == 1) {
			t.Fatal("initial cleanup did not follow its page boundary", pass, complete, err)
		}
	}
	read := func() model.Gateway {
		value, err := f.storage.GetRetained(ctx, "Gateway", gateway.ID)
		if err != nil {
			t.Fatal(err)
		}
		row, ok := value.(model.Gateway)
		if !ok {
			t.Fatal("retained Gateway type differs")
		}
		return row
	}
	before := read()
	states, err := before.CleanupObservations()
	if err != nil || !states["accounts"] || provider.calls != 101 || provider.inventory != 1 {
		t.Fatal("initial cleanup proof is absent", states, provider.calls, provider.inventory, err)
	}
	membership, err := f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scope)
	if err != nil || !membership.Sealed {
		t.Fatal("completed account scope is not sealed", membership, err)
	}
	if mode == "changed" {
		if _, err := f.db.ExecContext(ctx, "UPDATE gateways SET name=name || '-changed' WHERE id=$1", gateway.ID); err != nil {
			t.Fatal(err)
		}
		changed := read()
		states, err := changed.CleanupObservations()
		if err != nil || states["accounts"] || changed.ResourceGeneration != before.ResourceGeneration+1 {
			t.Fatal("changed inputs retained the old cleanup proof", states, err)
		}
		before = changed
	}
	if mode == "failure" {
		provider.failure = errors.New("provider verification failed")
	}
	// Reconstruct the service. Only durable state can supply the earlier proof.
	accounts, err = serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	passComplete, err := accounts.RecoverGatewayCleanup(ctx, gateway.ID)
	if mode == "failure" {
		if passComplete || !errors.Is(err, provider.failure) {
			t.Fatal("provider verification failure was lost", passComplete, err)
		}
	} else if err != nil {
		t.Fatal("clean verification failed", err)
	}
	if provider.calls != 201 || provider.inventory != 1 {
		t.Fatal("clean verification did not stop at one page", passComplete, provider.calls, provider.inventory, err)
	}
	after := read()
	current, err := f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scope)
	if err != nil || current != membership || after.ResourceGeneration != before.ResourceGeneration {
		t.Fatal("the verification inputs changed", current, membership, err)
	}
	states, err = after.CleanupObservations()
	wantProof := mode == "clean"
	if err != nil || states["accounts"] != wantProof {
		t.Fatal("verification did not preserve or invalidate the cleanup proof as required", mode, states, err)
	}
	if wantProof && after.ResourceVersion != before.ResourceVersion {
		t.Fatal("clean partial verification changed the resource version")
	}
}
