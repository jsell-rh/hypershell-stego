package acceptance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

// The allocator removes retained state after workload and SQL cleanup. Their
// observations cannot prove that the allocator has completed its work.
func TestGatewayFinalizationWaitsForAllocationCleanup(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	owner := principal("alice", "gateway:creator")
	gateway, err := f.service.Create(ctx, owner, f.request("allocation-pending"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Delete(ctx, owner, gateway.ID); err != nil {
		t.Fatal(err)
	}
	// No allocator runs in this fixture. Supply each existing owner's completed
	// observation. Retained namespace removal still has no completion record.
	for _, name := range []string{"accounts", "identity", "workload", "sql"} {
		err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
			value, err := tx.(store.RetainedReader).GetRetained(ctx, "Gateway", gateway.ID)
			if err != nil {
				return err
			}
			row := value.(model.Gateway)
			target := ""
			if name == "workload" || name == "sql" {
				target = row.ClusterID
			}
			return gateways.RecordCleanup(ctx, tx, row.ID, row.ResourceVersion, name, target, true)
		})
		if err != nil {
			t.Fatal("cleanup observation", name, err)
		}
	}
	// Read committed state after service reconstruction. This does not claim
	// process restart or Kubernetes coverage; those require the workflow gate.
	service, err := gateways.New(f.storage, gateways.Options{})
	if err != nil {
		t.Fatal(err)
	}
	var finalized bool
	var events int
	if err := f.db.QueryRowContext(ctx, "SELECT stego_finalized_at IS NOT NULL FROM gateways WHERE id=$1", gateway.ID).Scan(&finalized); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM stego_outbox.messages WHERE resource_key=$1 AND kind='gateway.deleted'", gateway.ID).Scan(&events); err != nil {
		t.Fatal(err)
	}
	if finalized || events != 0 {
		t.Fatalf("Gateway finalized before allocation cleanup: finalized=%t deletion_events=%d", finalized, events)
	}
	row, err := service.Get(ctx, owner, gateway.ID)
	if err != nil || !row.DeletedAt.Valid || row.DeletionFinalizedAt != nil {
		t.Fatal("Gateway must remain visible while allocation cleanup is pending", err)
	}
	allocator := principal("allocator")
	policy, err := auth.NewGrantPolicy([]auth.Grant{{Issuer: allocator.Issuer, Subject: allocator.Subject, Resource: "Gateway", Operation: "cleanup.allocation", Target: gateway.ClusterID}})
	if err != nil {
		t.Fatal(err)
	}
	service, err = gateways.New(f.storage, gateways.Options{ControlPlaneSubjects: []string{allocator.Subject, "workload"}, CleanupPolicy: policy})
	if err != nil {
		t.Fatal(err)
	}
	version := row.ResourceVersion
	for _, tc := range []struct {
		name    string
		caller  gateways.Principal
		target  string
		version int64
		want    error
	}{
		{"owner", owner, gateway.ClusterID, version, gateways.ErrForbidden},
		{"other controller", principal("workload"), gateway.ClusterID, version, gateways.ErrForbidden},
		{"other cluster", allocator, ksuid.New().String(), version, gateways.ErrForbidden},
		{"stale version", allocator, gateway.ClusterID, version - 1, store.ErrVersionConflict},
	} {
		err := service.ObserveCleanup(ctx, tc.caller, gateway.ID, tc.version, "allocation", tc.target, true)
		if !errors.Is(err, tc.want) {
			t.Fatal(tc.name, err)
		}
	}
	current, err := service.Get(ctx, owner, gateway.ID)
	if err != nil || current.ResourceVersion != version || current.DeletionFinalizedAt != nil {
		t.Fatal("denied allocation observation changed Gateway state", err)
	}
	// Finalization and its event must commit together. The same observation
	// must remain safe to retry after a failed event transaction.
	if _, err := f.db.ExecContext(ctx, "ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_allocation_event CHECK (kind <> 'gateway.deleted') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if err := service.ObserveCleanup(ctx, allocator, gateway.ID, version, "allocation", gateway.ClusterID, true); err == nil {
		t.Fatal("failed event transaction completed allocation cleanup")
	}
	current, err = service.Get(ctx, owner, gateway.ID)
	if err != nil || current.ResourceVersion != version || current.DeletionFinalizedAt != nil {
		t.Fatal("failed allocation event changed Gateway state", err)
	}
	if _, err := f.db.ExecContext(ctx, "ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_allocation_event"); err != nil {
		t.Fatal(err)
	}
	if err := service.ObserveCleanup(ctx, allocator, gateway.ID, version, "allocation", gateway.ClusterID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Get(ctx, owner, gateway.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatal("completed allocation did not finalize Gateway", err)
	}
	retained, err := service.IdentityState(ctx, allocator, gateway.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ObserveCleanup(ctx, allocator, gateway.ID, retained.ResourceVersion, "allocation", gateway.ClusterID, true); err != nil {
		t.Fatal(err)
	}
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM stego_outbox.messages WHERE resource_key=$1 AND kind='gateway.deleted'", gateway.ID).Scan(&events); err != nil || events != 1 {
		t.Fatal("allocation finalization must publish one deletion event", events, err)
	}

}
