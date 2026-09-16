package acceptance

import (
	"context"
	"net/url"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Normal deletion must finish before test fixture cleanup runs.
func (w *browserGatewayWorkload) checkSuppliedDatabaseRetention(operator *consoleBrowser, consumer *kgo.Client) {
	w.t.Helper()
	if response := operator.api(w.t, "DELETE", "/managed_clusters/"+w.f.cluster, nil); response.StatusCode != 409 {
		w.t.Fatal("cluster deletion did not protect the remaining Gateway", response.StatusCode)
	}
	for _, id := range w.gatewayIDs {
		response := w.owner.api(w.t, "GET", "/gateways/"+id, nil)
		if response.StatusCode == 404 {
			continue
		}
		if response.StatusCode != 200 {
			w.t.Fatal("remaining Gateway read failed", response.StatusCode)
		}
		if response = w.owner.api(w.t, "DELETE", "/gateways/"+id, nil); response.StatusCode != 202 {
			w.t.Fatal("remaining Gateway deletion failed", response.StatusCode)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	allocator, err := allocation.New(w.kubernetes, w.p.namespace)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, id := range w.gatewayIDs {
		w.awaitGatewayCleanup(ctx, allocator, id)
		w.requireSQLAbsent(ctx, w.databaseOptions, id)
	}
	w.requireInstallationData(ctx)
	if response := operator.api(w.t, "DELETE", "/managed_clusters/"+w.f.cluster, nil); response.StatusCode != 204 {
		w.t.Fatal("finished Gateway cleanup did not release its cluster", response.StatusCode)
	}
	readCatalogEvent(w.t, consumer, w.f.cluster, "ManagedClusters", "Delete", "managedcluster.deleted")
	w.t.Log("Browser deletion removed all Gateway SQL and state; the supplied PostgreSQL server and installation data remain")
}

func (w *browserGatewayWorkload) awaitGatewayCleanup(ctx context.Context, allocator *allocation.Allocator, id string) {
	w.t.Helper()
	ns, err := gatewayworkload.Namespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	state, err := gatewayworkload.StateNamespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	for {
		gone, err := allocator.NamespaceGone(ctx, "gateway", ns, id)
		if err != nil {
			w.t.Fatal("Gateway namespace read failed", err)
		}
		stateGone, err := allocator.NamespaceGone(ctx, "gateway-state", state, id)
		if err != nil {
			w.t.Fatal("Gateway state namespace read failed", err)
		}
		var complete bool
		err = w.f.db.QueryRowContext(ctx, `SELECT COALESCE(deleted_at IS NOT NULL AND stego_finalized_at IS NOT NULL AND stego_cleanup->>'accounts'='true' AND stego_cleanup->>'identity'='true' AND stego_cleanup_targets->'workload'->>$2='true' AND stego_cleanup_targets->'sql'->>$2='true',false) FROM gateways WHERE id=$1`, id, w.f.cluster).Scan(&complete)
		if err != nil {
			w.t.Fatal("Gateway cleanup read failed", err)
		}
		if gone && stateGone && complete {
			break
		}
		select {
		case <-ctx.Done():
			w.t.Fatal("Gateway SQL and workload cleanup did not finish")
		case <-time.After(time.Second):
		}
	}
	for _, profile := range []string{"gateway", "gateway-state"} {
		w.checkNoAllocationBindings(ctx, allocator, profile, id)
	}
}

func (w *browserGatewayWorkload) checkNoAllocationBindings(ctx context.Context, allocator *allocation.Allocator, profile, id string) {
	w.t.Helper()
	selector := allocation.MarkerLabel + "=" + allocator.Marker() + "," + allocation.ProfileLabel + "=" + profile + ",hypershell.redhat.io/gateway-id=" + id
	bindings, code, err := w.kubernetes.Request(ctx, "GET", "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings?limit=64&labelSelector="+url.QueryEscape(selector), nil)
	entries, ok := kube.Nested(bindings, "items").([]any)
	if err != nil || code != 200 || !ok || len(entries) != 0 || kube.String(bindings, "metadata", "continue") != "" {
		w.t.Fatal("allocation cluster bindings remain after cleanup", profile, code)
	}
}
