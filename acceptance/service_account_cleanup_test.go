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

type slowCleanupProvider struct {
	*accountProvider
	slow map[string]bool
}

func (p *slowCleanupProvider) Delete(ctx context.Context, gatewayID, id, uuid string) error {
	if p.slow[id] {
		<-ctx.Done()
		return ctx.Err()
	}
	return p.accountProvider.Delete(ctx, gatewayID, id, uuid)
}

func TestServiceAccountRecoveryResumesPartialPage(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	ctx := context.Background()
	live, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("live"))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 17; i++ {
		id := ksuid.New().String()
		row := model.ServiceAccount{Meta: model.Meta{ID: id}, GatewayID: gateway.ID, Name: fmt.Sprintf("partial-%d", i), CredentialType: "client_secret", Role: serviceaccounts.RoleUser, Status: "error", CreatedByUserID: live.Account.CreatedByUserID, ClientID: "hs-sa-" + gateway.ID + "-" + id, ExpiresAt: time.Now().Add(time.Hour)}
		if err := f.storage.Create(ctx, "ServiceAccount", row); err != nil {
			t.Fatal(err)
		}
		if err := f.storage.Delete(ctx, "ServiceAccount", id); err != nil {
			t.Fatal(err)
		}
		provider.clients[id] = serviceaccounts.Credential{ClientID: row.ClientID}
	}
	// Use the database's cursor order, which need not match Go string ordering.
	result, err := f.storage.List(ctx, "ServiceAccount", "status", "error", contract.ListOptions{Page: 1, Size: 100, IncludeDeleted: true, OrderBy: []contract.OrderByField{{Field: "id", Direction: "asc"}}})
	if err != nil {
		t.Fatal(err)
	}
	rows := result.Items.([]model.ServiceAccount)
	if len(rows) != 17 {
		t.Fatal("unexpected cleanup fixture size")
	}
	slow := map[string]bool{}
	for _, row := range rows[:8] {
		slow[row.ID] = true
	}
	target := rows[len(rows)-1].ID
	worker, err := serviceaccounts.New(f.storage, &slowCleanupProvider{accountProvider: provider, slow: slow})
	if err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(run) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("recovery did not stop")
		}
	})
	deadline := time.Now().Add(20 * time.Second)
	for {
		provider.mu.Lock()
		_, present := provider.clients[target]
		_, livePresent := provider.clients[live.Account.ID]
		provider.mu.Unlock()
		if !livePresent {
			t.Fatal("recovery removed a live account")
		}
		if !present {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("slow first workers prevented later records in the partial page from receiving a turn")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
