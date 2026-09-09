package acceptance

import (
	"context"
	"errors"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
)

func TestServiceAccountRequiresCurrentGatewayObservation(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	ctx := context.Background()
	name := "changed-intent"
	if _, err := f.service.Update(ctx, principal("alice"), gateway.ID, gateways.PatchRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("pending")); !errors.Is(err, serviceaccounts.ErrNotReady) {
		t.Fatal("stale health permitted credential creation", err)
	}
	if provider.calls != 0 || count(t, f.db, "service_accounts") != 0 {
		t.Fatal("pending observation reached the credential provider")
	}
	observeGatewayFixture(t, f, gateway.ID)
	if _, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("confirmed")); err != nil {
		t.Fatal(err)
	}
}
