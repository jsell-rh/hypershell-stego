package acceptance

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

// A fixed interruption after one confirmed item models a request budget without
// sleeping or imposing load. Inventory has no clients; retained IDs still need
// closure because a provider create can have completed after an earlier scan.
type interruptedGatewayCleanup struct {
	*accountProvider
	calls     int
	confirmed map[string]int
	cancel    context.CancelFunc
}

func (p *interruptedGatewayCleanup) DeleteGateway(context.Context, string) error { return nil }
func (p *interruptedGatewayCleanup) Delete(ctx context.Context, gatewayID, id, uuid string) error {
	p.calls++
	if p.cancel != nil && p.calls == 2 {
		p.cancel()
		return context.Canceled
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p.confirmed[id]++
	return p.accountProvider.Delete(ctx, gatewayID, id, uuid)
}

func TestGatewayAccountCleanupProgressSurvivesInterruptedRequests(t *testing.T) {
	f := database(t)
	original := newAccountProvider()
	accounts, gateway := accountService(t, f, original)
	live, err := accounts.Create(context.Background(), principal("alice"), gateway.ID, accountInput("history-source"))
	if err != nil {
		t.Fatal(err)
	}
	// Use only three rows. The requirement concerns durable progress, not capacity.
	ids := []string{live.Account.ID}
	for i := 0; i < 2; i++ {
		id := ksuid.New().String()
		row := model.ServiceAccount{Meta: model.Meta{ID: id}, GatewayID: gateway.ID,
			Name: fmt.Sprintf("retained-%d", i), CredentialType: "client_secret", Role: serviceaccounts.RoleUser,
			Status: "error", CreatedByUserID: live.Account.CreatedByUserID,
			ClientID: "hs-sa-" + gateway.ID + "-" + id, ExpiresAt: time.Now().Add(time.Hour)}
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
	provider := &interruptedGatewayCleanup{accountProvider: original, confirmed: map[string]int{}}
	for attempt := 0; attempt < 3; attempt++ {
		// Reconstruct both application services. Progress cannot live in their memory.
		cleanup, err := serviceaccounts.New(f.storage, provider)
		if err != nil {
			t.Fatal(err)
		}
		service, err := gateways.New(f.storage, gateways.Options{AccountCleaner: cleanup})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		provider.calls, provider.cancel = 0, cancel
		err = service.Delete(ctx, principal("alice"), gateway.ID)
		cancel()
		if err == nil {
			t.Fatal("interrupted cleanup reported Gateway deletion")
		}
		if !errors.Is(err, gateways.ErrGatewayCleanupUnavailable) && !errors.Is(err, context.Canceled) {
			t.Fatal("unexpected interrupted cleanup result", err)
		}
		if _, err := f.storage.Get(context.Background(), "Gateway", gateway.ID); err != nil {
			t.Fatal("interruption removed Gateway", err)
		}
	}
	visited := len(provider.confirmed)
	t.Logf("Three interrupted requests confirmed %d of %d retained accounts", visited, len(ids))
	// Restore the provider and verify normal cleanup before reporting the failure.
	provider.cancel = nil
	cleanup, err := serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	service, err := gateways.New(f.storage, gateways.Options{AccountCleaner: cleanup})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), principal("alice"), gateway.ID); err != nil {
		t.Fatal("uninterrupted cleanup", err)
	}
	if visited != len(ids) {
		t.Fatalf("cleanup repeated its completed prefix across service restart: reached %d of %d retained accounts", visited, len(ids))
	}
}
