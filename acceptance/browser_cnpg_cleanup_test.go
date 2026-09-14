package acceptance

import (
	"context"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// This removes test resources after the workflow. Normal Gateway deletion is
// checked separately through REST and the generated workers.
func (w *browserGatewayWorkload) cleanupCNPGFixture(ctx context.Context, allocator *allocation.Allocator, namespace, databaseID string) error {
	if gone, err := allocator.NamespaceGone(ctx, "database", namespace, databaseID); err != nil || gone {
		return err
	}
	if err := allocator.RequireNamespace(ctx, "database", namespace, databaseID); err != nil {
		return err
	}
	remove := func(path string, owner kube.Owner) error {
		for {
			gone, err := w.kubernetes.DeleteOwned(ctx, path, owner)
			if err != nil {
				return err
			}
			if gone {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Second):
			}
		}
	}
	base := "/apis/postgresql.cnpg.io/v1/namespaces/" + namespace
	// Let Database finalizers use the running server and operator permissions.
	for _, id := range w.gatewayIDs {
		owner := kube.Owner{"hypershell.redhat.io/gateway-id": id, "hypershell.redhat.io/database-id": databaseID, "app.kubernetes.io/managed-by": "hypershell-gateway-controller"}
		if err := remove(base+"/databases/"+cnpgGatewayName(id), owner); err != nil {
			return err
		}
	}
	return remove(base+"/clusters/openshell-db", kube.Owner{"hypershell.redhat.io/database-id": databaseID, "app.kubernetes.io/managed-by": "hypershell-database-controller"})
}
