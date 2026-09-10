package acceptance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	contract "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Every provider update reads this state. Totals are not part of that contract.
func TestIdentityUserStateUsesBoundedReadsWithoutTotals(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("identity-query"))
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:owner")
	owner, err := f.service.CreateGrant(ctx, principal("alice"), input)
	if err != nil {
		t.Fatal(err)
	}
	viewerInput := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	viewer, err := f.service.CreateGrant(ctx, principal("alice"), viewerInput)
	if err != nil {
		t.Fatal(err)
	}
	queries := &recoveryQueryLog{Interface: logger.Default.LogMode(logger.Silent)}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: f.db}), &gorm.Config{Logger: queries})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := model.NewStore(orm)
	if err != nil {
		t.Fatal(err)
	}
	service, err := gateways.New(repository, gateways.Options{ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		t.Fatal(err)
	}
	check := func(name, role string, reads int64) {
		t.Helper()
		t.Run(name, func(t *testing.T) {
			queries.reads.Store(0)
			queries.counts.Store(0)
			state, err := service.IdentityUserState(ctx, principal("controller"), gateway.ID, input.UserID)
			if err != nil || state.Role != role || state.UserID != input.UserID || state.GatewayID != gateway.ID || state.Issuer != "https://issuer.example" || state.Subject != "bob" {
				t.Fatal("incorrect identity state", state, err)
			}
			if queries.counts.Load() != 0 || queries.reads.Load() != reads {
				t.Fatalf("identity state ran %d counts and %d reads; want no count and %d reads", queries.counts.Load(), queries.reads.Load(), reads)
			}
		})
	}
	check("owner", "gateway:owner", 4)
	for _, p := range []gateways.Principal{principal("alice"), principal("bob"), principal("admin", "platform:admin")} {
		queries.reads.Store(0)
		state, err := service.IdentityUserState(ctx, p, gateway.ID, input.UserID)
		if !errors.Is(err, gateways.ErrForbidden) || state.UserID != "" || queries.reads.Load() != 0 {
			t.Fatal("denied read reached storage", err)
		}
	}
	if err := f.service.DeleteGrant(ctx, principal("alice"), owner.ID); err != nil {
		t.Fatal(err)
	}
	check("viewer", "gateway:viewer", 6)
	if err := f.service.DeleteGrant(ctx, principal("alice"), viewer.ID); err != nil {
		t.Fatal(err)
	}
	check("removed", "", 6)
	// A current grant for a different Gateway must not restore this user's role.
	other, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("identity-other"))
	if err != nil {
		t.Fatal(err)
	}
	input.GatewayID = other.ID
	if _, err := f.service.CreateGrant(ctx, principal("alice"), input); err != nil {
		t.Fatal(err)
	}
	check("other Gateway", "", 6)
	if _, err := f.db.ExecContext(ctx, "UPDATE users SET deleted_at=now() WHERE id=$1", input.UserID); err != nil {
		t.Fatal(err)
	}
	check("deleted user", "", 2)
	if _, err := f.db.ExecContext(ctx, "UPDATE users SET issuer=NULL,subject=NULL WHERE id=$1", input.UserID); err != nil {
		t.Fatal(err)
	}
	queries.reads.Store(0)
	queries.counts.Store(0)
	state, err := service.IdentityUserState(ctx, principal("controller"), gateway.ID, input.UserID)
	if !errors.Is(err, gateways.ErrUnboundUser) || state.UserID != "" || queries.reads.Load() != 2 || queries.counts.Load() != 0 {
		t.Fatal("unbound user did not fail with one user read", err)
	}
	queries.reads.Store(0)
	state, err = service.IdentityUserState(ctx, principal("controller"), gateway.ID, ksuid.New().String())
	if !errors.Is(err, contract.ErrNotFound) || state.UserID != "" || queries.reads.Load() != 2 {
		t.Fatal("absent user supplied cleanup state", err, queries.reads.Load())
	}
}

// A missing role definition is an error, not evidence that access was removed.
func TestIdentityUserStateRejectsMissingRoleDefinition(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("identity-role"))
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	if _, err := f.service.CreateGrant(ctx, principal("alice"), input); err != nil {
		t.Fatal(err)
	}
	service, err := gateways.New(f.storage, gateways.Options{ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, "UPDATE roles SET deleted_at=now() WHERE name='gateway:owner'"); err != nil {
		t.Fatal(err)
	}
	state, err := service.IdentityUserState(ctx, principal("controller"), gateway.ID, input.UserID)
	if err == nil || state != (gateways.IdentityUser{}) {
		t.Fatal("missing role definition supplied state", state, err)
	}
}
