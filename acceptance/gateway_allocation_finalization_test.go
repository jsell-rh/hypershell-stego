package acceptance

import (
	"context"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
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
	if err := f.db.QueryRowContext(ctx, "SELECT deletion_finalized_at IS NOT NULL FROM gateways WHERE id=$1", gateway.ID).Scan(&finalized); err != nil {
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
}
