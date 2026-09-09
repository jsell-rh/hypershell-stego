package acceptance

import (
	"context"
	"errors"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"testing"
)

func TestIdentityUserStateUsesCurrentGrantsAndRetainsRemovalTargets(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	service, err := gateways.New(f.storage, gateways.Options{ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		t.Fatal(err)
	}
	gateway, err := service.Create(ctx, principal("alice", "gateway:creator"), f.request("user-state"))
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	viewer, err := service.CreateGrant(ctx, principal("alice"), input)
	if err != nil {
		t.Fatal(err)
	}
	ownerInput := grantInput(t, f, gateway.ID, "bob", "gateway:owner")
	owner, err := service.CreateGrant(ctx, principal("alice"), ownerInput)
	if err != nil {
		t.Fatal(err)
	}
	reader := principal("controller")
	check := func(want string) {
		t.Helper()
		state, err := service.IdentityUserState(ctx, reader, gateway.ID, input.UserID)
		if err != nil || state.Role != want || state.Issuer != "https://issuer.example" || state.Subject != "bob" || state.GatewayID != gateway.ID || state.UserID != input.UserID {
			t.Fatalf("user state: %+v %v", state, err)
		}
	}
	check("gateway:owner")
	if err := service.DeleteGrant(ctx, principal("alice"), owner.ID); err != nil {
		t.Fatal(err)
	}
	check("gateway:viewer")
	if err := service.DeleteGrant(ctx, principal("alice"), viewer.ID); err != nil {
		t.Fatal(err)
	}
	check("")
	ids, more, err := service.IdentityUsers(ctx, reader, gateway.ID, 1)
	if err != nil || more {
		t.Fatal("identity references", err)
	}
	found := false
	for _, id := range ids {
		found = found || id == input.UserID
	}
	if !found {
		t.Fatal("removed user was absent from recovery inventory")
	}
	for _, p := range []gateways.Principal{principal("alice"), principal("bob"), principal("admin", "platform:admin")} {
		state, err := service.IdentityUserState(ctx, p, gateway.ID, input.UserID)
		if !errors.Is(err, gateways.ErrForbidden) || state.UserID != "" {
			t.Fatal("non-controller read identity state", err)
		}
		if ids, _, err := service.IdentityUsers(ctx, p, gateway.ID, 1); !errors.Is(err, gateways.ErrForbidden) || len(ids) != 0 {
			t.Fatal("non-controller read identity inventory", err)
		}
	}
	if _, _, err := service.IdentityUsers(ctx, reader, gateway.ID, 0); !errors.Is(err, gateways.ErrInvalid) {
		t.Fatal("invalid page", err)
	}
	if _, err := f.db.Exec("UPDATE users SET username='changed-profile' WHERE id=$1", input.UserID); err != nil {
		t.Fatal(err)
	}
	check("")
	if _, err := service.CreateGrant(ctx, principal("alice"), input); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("UPDATE users SET deleted_at=now() WHERE id=$1", input.UserID); err != nil {
		t.Fatal(err)
	}
	check("")
	if _, err := f.db.Exec("UPDATE users SET issuer=NULL,subject=NULL WHERE id=$1", input.UserID); err != nil {
		t.Fatal(err)
	}
	if state, err := service.IdentityUserState(ctx, reader, gateway.ID, input.UserID); !errors.Is(err, gateways.ErrUnboundUser) || state.UserID != "" {
		t.Fatal("legacy profile supplied a provider identity", err)
	}
}
