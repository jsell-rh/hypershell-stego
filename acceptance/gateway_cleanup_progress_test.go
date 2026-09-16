package acceptance

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

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
	provider := &boundedCleanupProvider{accountProvider: original, confirmed: map[string]int{}}
	ctx, cancel := context.WithCancel(context.Background())
	if err := f.service.Delete(ctx, principal("alice"), gateway.ID); err != nil {
		cancel()
		t.Fatal(err)
	}
	cancel()
	if provider.calls != 0 || provider.inventory != 0 {
		t.Fatal("request ran provider cleanup")
	}
	// Reconstruct services after the accepted request context has ended.
	service, err := gateways.New(f.storage, gateways.Options{})
	if err != nil {
		t.Fatal(err)
	}
	cleanup, err := serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Delete(context.Background(), principal("alice"), gateway.ID); err != nil {
		t.Fatal("repeated request", err)
	}
	if complete, err := cleanup.RecoverGatewayCleanup(context.Background(), gateway.ID); err != nil || !complete {
		t.Fatal("independent cleanup", complete, err)
	}
	if len(provider.confirmed) != len(ids) || provider.inventory != 1 {
		t.Fatal("restart did not check retained accounts and inventory")
	}
	row, err := service.Get(context.Background(), principal("alice"), gateway.ID)
	if err != nil || !row.DeletedAt.Valid || row.DeletionFinalizedAt != nil {
		t.Fatal("account cleanup bypassed the other cleanup owners", err)
	}
}
