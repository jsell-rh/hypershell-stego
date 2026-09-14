package acceptance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	postgres "github.com/jsell-rh/hypershell-stego/out/postgres"
	"github.com/twmb/franz-go/pkg/kgo"
)

// Use the browser backend and running controllers for normal parent deletion.
// Fixture cleanup runs later and cannot satisfy these checks.
func (w *browserGatewayWorkload) checkDatabaseDeletion(operator *consoleBrowser, consumer *kgo.Client) {
	w.t.Helper()
	if response := operator.api(w.t, "DELETE", "/managed_databases/"+w.f.database, nil); response.StatusCode != 409 {
		w.t.Fatal("database deletion did not protect the remaining Gateway", response.StatusCode)
	}
	for _, id := range w.gatewayIDs {
		response := w.owner.api(w.t, "GET", "/gateways/"+id, nil)
		if response.StatusCode == 404 {
			continue
		}
		if response.StatusCode != 200 {
			w.t.Fatal("remaining Gateway read failed", response.StatusCode)
		}
		if response = w.owner.api(w.t, "DELETE", "/gateways/"+id, nil); response.StatusCode != 204 {
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
		ns, err := gatewayworkload.Namespace(id)
		if err != nil {
			w.t.Fatal(err)
		}
		for {
			gone, err := allocator.NamespaceGone(ctx, "gateway", ns, id)
			if err != nil {
				w.t.Fatal("Gateway namespace read failed", err)
			}
			var complete bool
			err = w.f.db.QueryRowContext(ctx, `SELECT COALESCE(deleted_at IS NOT NULL AND stego_cleanup->>'identity'='true' AND stego_cleanup->>'workload'='true',false) FROM gateways WHERE id=$1`, id).Scan(&complete)
			if err != nil {
				w.t.Fatal("Gateway cleanup read failed", err)
			}
			if gone && complete {
				break
			}
			select {
			case <-ctx.Done():
				w.t.Fatal("last Gateway cleanup did not finish")
			case <-time.After(time.Second):
			}
		}
		w.checkNoAllocationBindings(ctx, allocator, "gateway", id)
	}
	ns, err := gateways.DatabaseNamespace(w.f.database)
	if err != nil {
		w.t.Fatal(err)
	}
	if err := allocator.RequireNamespace(ctx, "database", ns, w.f.database); err != nil {
		w.t.Fatal("database namespace disappeared before parent deletion", err)
	}
	w.checkAllGatewaySQLAbsent(ctx, ns)
	objects, code, err := w.kubernetes.Request(ctx, "GET", "/apis/postgresql.cnpg.io/v1/namespaces/"+ns+"/databases?limit=64", nil)
	entries, ok := kube.Nested(objects, "items").([]any)
	if err != nil || code != 200 || !ok || len(entries) != 0 || kube.String(objects, "metadata", "continue") != "" {
		w.t.Fatal("Gateway database resources remain before server deletion", code)
	}
	claims, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+ns+"/persistentvolumeclaims?limit=64", nil)
	entries, ok = kube.Nested(claims, "items").([]any)
	if err != nil || code != 200 || !ok || len(entries) == 0 || kube.String(claims, "metadata", "continue") != "" {
		w.t.Fatal("CNPG storage evidence is unavailable", code)
	}
	type volume struct{ Claim, UID, Volume string }
	volumes := make([]volume, 0, len(entries))
	for _, raw := range entries {
		claim, ok := raw.(map[string]any)
		v := volume{kube.String(claim, "metadata", "name"), kube.String(claim, "metadata", "uid"), kube.String(claim, "spec", "volumeName")}
		if !ok || v.Claim == "" || v.UID == "" || v.Volume == "" {
			w.t.Fatal("CNPG storage has no bound identity")
		}
		volumes = append(volumes, v)
	}
	// The host can check named PV deletion without granting PV access to workers.
	if dir := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); dir != "" {
		data, err := json.MarshalIndent(struct {
			Namespace string
			Volumes   []volume
		}{ns, volumes}, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(dir, "database-deletion-storage.json"), data, 0600) != nil {
			w.t.Fatal("cannot write database storage evidence")
		}
	}
	if response := operator.api(w.t, "DELETE", "/managed_databases/"+w.f.database, nil); response.StatusCode != 204 {
		w.t.Fatal("finished Gateway cleanup did not release the database", response.StatusCode)
	}
	readCatalogEvent(w.t, consumer, w.f.database, "ManagedDatabases", "Delete", "manageddatabase.deleted")
	for {
		gone, err := allocator.NamespaceGone(ctx, "database", ns, w.f.database)
		if err != nil {
			w.t.Fatal("database namespace read failed", err)
		}
		var complete bool
		err = w.f.db.QueryRowContext(ctx, `SELECT COALESCE(deleted_at IS NOT NULL AND stego_cleanup->>'provider'='true',false) FROM managed_databases WHERE id=$1`, w.f.database).Scan(&complete)
		if err != nil {
			w.t.Fatal("database cleanup read failed", err)
		}
		if gone && complete {
			break
		}
		select {
		case <-ctx.Done():
			w.t.Fatal("database deletion did not finish namespace and provider cleanup")
		case <-time.After(time.Second):
		}
	}
	w.checkNoAllocationBindings(ctx, allocator, "database", w.f.database)
	if response := operator.api(w.t, "GET", "/managed_databases/"+w.f.database, nil); response.StatusCode != 404 {
		w.t.Fatal("deleted database remained public", response.StatusCode)
	}
	if response := operator.api(w.t, "DELETE", "/managed_clusters/"+w.f.cluster, nil); response.StatusCode != 204 {
		w.t.Fatal("finished database cleanup did not release its cluster", response.StatusCode)
	}
	readCatalogEvent(w.t, consumer, w.f.cluster, "ManagedClusters", "Delete", "managedcluster.deleted")
	w.t.Log("Browser API deletion removed the last Gateway and CNPG namespace; generated controllers confirmed cleanup and released the cluster")
}

func (w *browserGatewayWorkload) checkNoAllocationBindings(ctx context.Context, allocator *allocation.Allocator, profile, id string) {
	w.t.Helper()
	selector := "stego.dev/allocator=" + allocator.Marker() + ",hypershell.redhat.io/" + profile + "-id=" + id
	bindings, code, err := w.kubernetes.Request(ctx, "GET", "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings?limit=64&labelSelector="+url.QueryEscape(selector), nil)
	entries, ok := kube.Nested(bindings, "items").([]any)
	if err != nil || code != 200 || !ok || len(entries) != 0 || kube.String(bindings, "metadata", "continue") != "" {
		w.t.Fatal("allocation cluster bindings remain after cleanup", profile, code)
	}
}

func (w *browserGatewayWorkload) checkAllGatewaySQLAbsent(ctx context.Context, ns string) {
	w.t.Helper()
	read := func(name, field string) string {
		secret, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+ns+"/secrets/"+name, nil)
		if err != nil || code != 200 {
			w.t.Fatal("CNPG cleanup check credentials are unavailable", code)
		}
		value, err := base64.StdEncoding.DecodeString(kube.String(secret, "data", field))
		if err != nil || len(value) == 0 {
			w.t.Fatal("CNPG cleanup check credential field is unavailable")
		}
		return string(value)
	}
	if read("openshell-db-app", "username") != "openshell" {
		w.t.Fatal("CNPG cleanup check has an unexpected user")
	}
	o := postgres.Options{Host: "openshell-db-rw." + ns + ".svc.cluster.local", Port: 5432, User: "openshell", Database: "openshell", Password: read("openshell-db-app", "password"), CA: []byte(read("openshell-db-ca", "ca.crt"))}
	for _, id := range w.gatewayIDs {
		var absent bool
		if err := postgres.ReadRow(ctx, o, `SELECT NOT EXISTS(SELECT 1 FROM pg_catalog.pg_roles WHERE rolname=$1) AND NOT EXISTS(SELECT 1 FROM pg_catalog.pg_database WHERE datname=$1)`, []any{cnpgGatewayRole(id)}, &absent); err != nil || !absent {
			w.t.Fatal("Gateway SQL state remains before server deletion", err)
		}
		for _, path := range []string{"secrets/" + cnpgGatewayName(id) + "-credentials", "secrets/" + cnpgGatewayName(id) + "-keys", "configmaps/" + cnpgGatewayName(id) + "-key-identity"} {
			if _, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+ns+"/"+path, nil); err != nil || code != 404 {
				w.t.Fatal("Gateway credentials remain before server deletion", code)
			}
		}
	}
}
