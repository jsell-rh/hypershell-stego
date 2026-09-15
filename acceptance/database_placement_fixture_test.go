package acceptance

import (
	"context"
	"testing"
	"time"
)

// Inject retained target history to check controller grants and cleanup.
// Public API requests cannot change an assigned Gateway cluster.
func restoreGatewayClusterHistory(t *testing.T, f *fixture, gateway, cluster string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := f.db.ExecContext(ctx, `UPDATE gateways SET cluster_id=$1 WHERE id=$2`, cluster, gateway)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal("target history requires one Gateway", n, err)
	}
}
