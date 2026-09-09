package acceptance

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	"github.com/segmentio/ksuid"
)

func grantInput(t testing.TB, f *fixture, gatewayID, username, role string) gateways.GrantRequest {
	t.Helper()
	if _, err := f.service.List(context.Background(), principal(username), 1, 1); err != nil {
		t.Fatal(err)
	}
	input := gateways.GrantRequest{GatewayID: gatewayID, Scope: "gateway"}
	if err := f.db.QueryRow("SELECT id FROM users WHERE issuer=$1 AND subject=$2", "https://issuer.example", username).Scan(&input.UserID); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRow("SELECT id FROM roles WHERE name=$1", role).Scan(&input.RoleID); err != nil {
		t.Fatal(err)
	}
	return input
}
func TestGatewayGrantCanBeRemovedAndGrantedAgain(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	owner := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("shared"))
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	first, err := f.service.CreateGrant(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Get(ctx, principal("bob"), gateway.ID); err != nil {
		t.Fatal("viewer cannot read shared Gateway", err)
	}
	if err := f.service.DeleteGrant(ctx, owner, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Get(ctx, principal("bob"), gateway.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("removed viewer retained access", err)
	}
	second, err := f.service.CreateGrant(ctx, owner, input)
	if err != nil {
		t.Fatal("cannot grant access again", err)
	}
	if first.ID == second.ID {
		t.Fatal("new grant reused an old event identity")
	}
	if _, err := f.service.Get(ctx, principal("bob"), gateway.ID); err != nil {
		t.Fatal("new grant did not restore access", err)
	}
	var deleted bool
	if err := f.db.QueryRow("SELECT deleted_at IS NOT NULL FROM role_bindings WHERE id=$1", first.ID).Scan(&deleted); err != nil || !deleted {
		t.Fatal("old grant history was lost")
	}
}
func TestConcurrentGrantRemovalPreservesLastOwner(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	owner := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("owners"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := f.service.CreateGrant(ctx, owner, grantInput(t, f, gateway.ID, "bob", "gateway:owner"))
	if err != nil {
		t.Fatal(err)
	}
	var first string
	if err := f.db.QueryRow("SELECT id FROM role_bindings WHERE gateway_id=$1 AND id<>$2", gateway.ID, second.ID).Scan(&first); err != nil {
		t.Fatal(err)
	}
	service, err := gateways.New(f.storage, gateways.Options{DatabaseProvider: gateways.ProviderCNPG, ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, id := range []string{first, second.ID} {
		workers.Go(func() { <-start; results <- service.DeleteGrant(ctx, principal("controller"), id) })
	}
	close(start)
	workers.Wait()
	close(results)
	success, denied := 0, 0
	for err := range results {
		if err == nil {
			success++
		} else if errors.Is(err, gateways.ErrLastOwner) {
			denied++
		} else {
			t.Fatal(err)
		}
	}
	if success != 1 || denied != 1 {
		t.Fatalf("owner removal results: success=%d denied=%d", success, denied)
	}
}

func TestGrantEventsCommitWithGrantChanges(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	owner := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("atomic-grants"))
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	// Reject the second event after the first event and the grant were written.
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_grant_update CHECK(kind <> 'gateway.updated')`); err != nil {
		t.Fatal(err)
	}
	grant, err := f.service.CreateGrant(ctx, owner, input)
	if err == nil || grant.ID != "" {
		t.Fatal("failed grant event reported success")
	}
	if _, err := f.service.Get(ctx, principal("bob"), gateway.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("failed grant exposed Gateway", err)
	}
	if count(t, f.db, "role_bindings") != 1 || count(t, f.db, "stego_outbox.messages") != 2 {
		t.Fatal("failed grant left partial state")
	}
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_grant_update`); err != nil {
		t.Fatal(err)
	}
	grant, err = f.service.CreateGrant(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_grant_delete CHECK(kind <> 'rolebinding.deleted')`); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeleteGrant(ctx, owner, grant.ID); err == nil {
		t.Fatal("failed delete event reported success")
	}
	if _, err := f.service.GetGrant(ctx, principal("bob"), grant.ID); err != nil {
		t.Fatal("failed delete removed grant", err)
	}
	if _, err := f.service.Get(ctx, principal("bob"), gateway.ID); err != nil {
		t.Fatal("failed delete removed access", err)
	}
	if count(t, f.db, "stego_outbox.messages") != 4 {
		t.Fatal("failed delete left an event")
	}
}

func TestLiveGrantMigrationPreservesDeletedRows(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	owner := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("grant-migration"))
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	first, err := f.service.CreateGrant(ctx, owner, input)
	if err != nil {
		t.Fatal(err)
	}
	// Recreate the previous full index on this private database.
	if _, err := f.db.Exec(`DROP INDEX stego_live_unique_a4b2f39ea673d046e72e82bd886a1565`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE UNIQUE INDEX composite_user_id_role_id_gateway_id ON role_bindings(user_id,role_id,gateway_id)`); err != nil {
		t.Fatal(err)
	}
	if err := f.service.DeleteGrant(ctx, owner, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CreateGrant(ctx, owner, input); !errors.Is(err, store.ErrConflict) {
		t.Fatal("old index did not reproduce conflict", err)
	}
	migration, err := os.ReadFile("../migrations/000003_live_gateway_grants.sql")
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := f.db.Exec(string(migration)); err != nil {
			t.Fatal("grant migration", err)
		}
	}
	second, err := f.service.CreateGrant(ctx, owner, input)
	if err != nil || second.ID == first.ID {
		t.Fatal("migration did not permit a new grant", err)
	}
	var deleted bool
	if err := f.db.QueryRow("SELECT deleted_at IS NOT NULL FROM role_bindings WHERE id=$1", first.ID).Scan(&deleted); err != nil || !deleted {
		t.Fatal("migration lost grant history", err)
	}
	if _, err := f.service.CreateGrant(ctx, owner, input); !errors.Is(err, store.ErrConflict) {
		t.Fatal("migration lost live uniqueness", err)
	}
}

func TestGrantRejectsInvalidTargets(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	owner := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("grant-targets"))
	if err != nil {
		t.Fatal(err)
	}
	valid := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	for _, change := range []func(*gateways.GrantRequest){
		func(v *gateways.GrantRequest) { v.Scope = "global" },
		func(v *gateways.GrantRequest) { v.RoleID = "" },
		func(v *gateways.GrantRequest) { v.UserID = "" },
		func(v *gateways.GrantRequest) { v.RoleID = ksuid.New().String() },
		func(v *gateways.GrantRequest) { v.UserID = ksuid.New().String() },
	} {
		input := valid
		change(&input)
		if result, err := f.service.CreateGrant(ctx, owner, input); !errors.Is(err, gateways.ErrInvalid) || result.ID != "" {
			t.Fatal("invalid target accepted", err)
		}
	}
	// A profile without a verified identity must not receive a new grant.
	if _, err := f.db.Exec("UPDATE users SET issuer=NULL,subject=NULL WHERE id=$1", valid.UserID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.CreateGrant(ctx, owner, valid); !errors.Is(err, gateways.ErrInvalid) {
		t.Fatal("unbound profile received grant", err)
	}
	if count(t, f.db, "role_bindings") != 1 || count(t, f.db, "stego_outbox.messages") != 2 {
		t.Fatal("invalid target changed grants or events")
	}
}
