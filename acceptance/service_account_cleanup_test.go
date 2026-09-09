package acceptance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	contract "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

type cleanupRetryProvider struct {
	*accountProvider
	target   string
	attempts int
}

func (p *cleanupRetryProvider) Delete(ctx context.Context, gatewayID, id, uuid string) error {
	if uuid != "" {
		return errors.New("deleted cleanup used an obsolete provider UUID")
	}
	p.mu.Lock()
	if id == p.target {
		p.attempts++
		if p.attempts == 1 {
			p.mu.Unlock()
			return errors.New("provider unavailable on first cleanup")
		}
	}
	p.mu.Unlock()
	return p.accountProvider.Delete(ctx, gatewayID, id, uuid)
}

func TestDeletedServiceAccountCleanupRetriesAcrossPages(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	live, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("live"))
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = ksuid.New().String()
	}
	slices.Sort(ids)
	for i, id := range ids {
		row := model.ServiceAccount{Meta: model.Meta{ID: id}, GatewayID: gateway.ID, Name: fmt.Sprintf("deleted-%d", i), CredentialType: "client_secret", Role: serviceaccounts.RoleUser, Status: "error", CreatedByUserID: live.Account.CreatedByUserID, ClientID: "hs-sa-" + gateway.ID + "-" + id, ClientUuid: "obsolete-provider-id", ExpiresAt: time.Now().Add(time.Hour)}
		if err := f.storage.Create(ctx, "ServiceAccount", row); err != nil {
			t.Fatal(err)
		}
		if err := f.storage.Delete(ctx, "ServiceAccount", id); err != nil {
			t.Fatal(err)
		}
		provider.clients[id] = serviceaccounts.Credential{ClientID: row.ClientID, ClientUUID: "late-provider-id"}
	}
	pendingID, err := ksuid.NewRandomWithTime(time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	pending := model.ServiceAccount{Meta: model.Meta{ID: pendingID.String()}, GatewayID: gateway.ID, Name: "pending-error", CredentialType: "client_secret", Role: serviceaccounts.RoleUser, Status: "error", CreatedByUserID: live.Account.CreatedByUserID, ClientID: "hs-sa-" + gateway.ID + "-" + pendingID.String(), ExpiresAt: time.Now().Add(time.Hour), Active: true}
	if err := f.storage.Create(ctx, "ServiceAccount", pending); err != nil {
		t.Fatal(err)
	}
	provider.clients[pending.ID] = serviceaccounts.Credential{ClientID: pending.ClientID}
	retry := &cleanupRetryProvider{accountProvider: provider, target: ids[0]}
	recovery, err := serviceaccounts.New(f.storage, retry)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- recovery.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("cleanup did not stop")
		}
	})
	urgentDeadline := time.Now().Add(5 * time.Second)
	for {
		provider.mu.Lock()
		_, present := provider.clients[pending.ID]
		provider.mu.Unlock()
		if !present {
			break
		}
		if time.Now().After(urgentDeadline) {
			t.Fatal("deleted history delayed pending cleanup")
		}
		time.Sleep(25 * time.Millisecond)
	}
	deadline := time.Now().Add(25 * time.Second)
	for {
		provider.mu.Lock()
		remaining := len(provider.clients)
		_, livePresent := provider.clients[live.Account.ID]
		attempts := retry.attempts
		provider.mu.Unlock()
		if !livePresent {
			t.Fatal("cleanup removed a live account")
		}
		if remaining == 1 && attempts >= 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("cleanup did not retry across pages: %d clients, %d target attempts", remaining, attempts)
		}
		time.Sleep(25 * time.Millisecond)
	}
	if _, err := f.storage.Get(ctx, "ServiceAccount", ids[0]); !errors.Is(err, contract.ErrNotFound) {
		t.Fatal("cleanup restored deleted metadata")
	}
	value, err := f.storage.Get(ctx, "ServiceAccount", live.Account.ID)
	if err != nil || value.(model.ServiceAccount).Status != "ready" {
		t.Fatal("cleanup changed a live account")
	}
}
