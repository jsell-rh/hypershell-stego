package acceptance

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// Check the actual ServiceAccount roles. No credential enters the evidence.
func (w *browserGatewayWorkload) checkAllocationAccess() {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	allocator, err := allocation.New(w.kubernetes, w.p.namespace)
	if err != nil {
		w.t.Fatal(err)
	}
	database, gateway := "", ""
	for ns, target := range w.allocations {
		if target.profile == "database" {
			database = ns
		} else {
			gateway = ns
		}
		if err := allocator.RequireNamespace(ctx, target.profile, ns, target.id); err != nil {
			w.t.Fatal("namespace was not allocated", err)
		}
		quota, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+ns+"/resourcequotas/stego-allocation", nil)
		if err != nil || code != 200 || kube.String(quota, "spec", "hard", "pods") != "2" || kube.String(quota, "spec", "hard", "limits.ephemeral-storage") != "512Mi" {
			w.t.Fatal("allocation limits missing", code)
		}
	}
	if database == "" || gateway == "" {
		w.t.Fatal("allocation targets missing")
	}
	type check struct {
		Worker, Namespace, Group, Resource, Verb string
		Allowed                                  bool
	}
	checks := []check{
		{"database", database, "", "secrets", "get", true},
		{"database", gateway, "", "secrets", "get", false},
		{"database", w.p.namespace, "", "secrets", "get", false},
		{"database", "", "", "namespaces", "create", false},
		{"database", "", "", "namespaces", "patch", false},
		{"gateway-workload", gateway, "apps", "deployments", "create", true},
		{"gateway-workload", database, "", "secrets", "get", true},
		{"gateway-workload", w.p.namespace, "", "secrets", "get", false},
		{"gateway-workload", "", "", "namespaces", "delete", false},
		{"gateway-workload", "", "rbac.authorization.k8s.io", "clusterroles", "create", false},
		{"gateway-workload", gateway, "rbac.authorization.k8s.io", "rolebindings", "create", false},
		{"namespace-allocation", database, "", "secrets", "get", false},
		{"namespace-allocation", gateway, "", "secrets", "get", false},
		{"namespace-allocation", w.p.namespace, "", "secrets", "get", false},
		{"gateway-identity", database, "", "secrets", "get", false},
	}
	clients := map[string]*kube.Client{}
	for _, test := range checks {
		client := clients[test.Worker]
		if client == nil {
			token, code, err := w.kubernetes.Request(ctx, "POST", "/api/v1/namespaces/"+w.p.namespace+"/serviceaccounts/hypershell-"+test.Worker+"/token", kube.Object{"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest", "spec": kube.Object{"expirationSeconds": 600}})
			if err != nil || code != 201 {
				w.t.Fatal("test identity request failed", code)
			}
			value := kube.String(token, "status", "token")
			if value == "" {
				w.t.Fatal("test identity is empty")
			}
			file := filepath.Join(w.t.TempDir(), "token")
			if err := os.WriteFile(file, []byte(value), 0600); err != nil {
				w.t.Fatal(err)
			}
			client, err = kube.New(kube.Options{ServerURL: w.options.ServerURL, CAFile: w.options.CAFile, TokenFile: file})
			if err != nil {
				w.t.Fatal("test client setup failed")
			}
			defer client.Close()
			clients[test.Worker] = client
		}
		review, code, err := client.Request(ctx, "POST", "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", kube.Object{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "spec": kube.Object{"resourceAttributes": kube.Object{"namespace": test.Namespace, "group": test.Group, "resource": test.Resource, "verb": test.Verb}}})
		allowed, ok := kube.Nested(review, "status", "allowed").(bool)
		if err != nil || code != 201 || !ok || allowed != test.Allowed {
			w.t.Fatal("worker access differs from allocation", test, code)
		}
	}
	// Server dry-run executes authorization and admission without storing changes.
	// https://kubernetes.io/docs/reference/using-api/api-concepts/#dry-run
	allocatorClient := clients["namespace-allocation"]
	probes := []struct {
		name, method, path string
		body               kube.Object
	}{
		{"foreign namespace", "POST", "/api/v1/namespaces?dryRun=All", kube.Object{"apiVersion": "v1", "kind": "Namespace", "metadata": kube.Object{"name": "stego-denied-namespace"}}},
		{"quota change", "PATCH", "/api/v1/namespaces/" + database + "/resourcequotas/stego-allocation?dryRun=All", kube.Object{"spec": kube.Object{"hard": kube.Object{"pods": "3"}}}},
		{"key identity change", "PATCH", "/api/v1/namespaces/" + database + "?dryRun=All", kube.Object{"metadata": kube.Object{"annotations": kube.Object{"hypershell.redhat.io/gateway-keys": "changed"}}}},
	}
	for _, probe := range probes {
		_, code, err := allocatorClient.Request(ctx, probe.method, probe.path, probe.body)
		if err == nil || code != 403 {
			w.t.Fatal("allocation admission did not deny change", probe.name, code)
		}
	}
	w.t.Log("Three server dry-run checks denied a foreign namespace, quota change, and key identity change")
	if dir := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); dir != "" {
		data, err := json.MarshalIndent(checks, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(dir, "allocation-permissions.json"), data, 0600) != nil {
			w.t.Fatal("cannot write allocation evidence")
		}
	}
	w.t.Log("Allocated namespaces have fixed limits; fifteen live worker access checks passed")
}

// A normal REST deletion must drive allocation cleanup and controller records.
func (w *browserGatewayWorkload) checkAllocatedDeletion(id string) {
	w.t.Helper()
	response := w.owner.api(w.t, "GET", "/gateways/"+id, nil)
	var gateway httpapi.Gateway
	if response.StatusCode != 200 || json.Unmarshal(response.Body, &gateway) != nil {
		w.t.Fatal("Gateway deletion setup failed")
	}
	databaseNamespace, err := gateways.DatabaseNamespace(gateway.DatabaseID)
	if err != nil {
		w.t.Fatal(err)
	}
	response = w.owner.api(w.t, "DELETE", "/gateways/"+id, nil)
	if response.StatusCode != 204 {
		w.t.Fatal("Gateway deletion failed", response.StatusCode)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	allocator, err := allocation.New(w.kubernetes, w.p.namespace)
	if err != nil {
		w.t.Fatal(err)
	}
	for {
		gatewayGone, gatewayErr := allocator.NamespaceGone(ctx, "gateway", gateway.Namespace, id)
		databaseGone, databaseErr := allocator.NamespaceGone(ctx, "database", databaseNamespace, gateway.DatabaseID)
		if gatewayErr != nil || databaseErr != nil {
			w.t.Fatal("allocation deletion observation failed", gatewayErr, databaseErr)
		}
		var complete bool
		err := w.f.db.QueryRowContext(ctx, `SELECT COALESCE(g.deleted_at IS NOT NULL AND d.deleted_at IS NOT NULL AND g.stego_cleanup->>'identity'='true' AND g.stego_cleanup_targets->'workload'->>$2='true' AND d.stego_cleanup->>'provider'='true',false) FROM gateways g JOIN managed_databases d ON d.id=g.database_id WHERE g.id=$1`, id, w.f.cluster).Scan(&complete)
		if err != nil {
			w.t.Fatal("cleanup record read failed", err)
		}
		if gatewayGone && databaseGone && complete {
			break
		}
		select {
		case <-ctx.Done():
			w.t.Fatal("REST deletion did not complete allocation and provider cleanup")
		case <-time.After(time.Second):
		}
	}
	selector := "stego.dev/allocator=" + allocator.Marker() + ",hypershell.redhat.io/gateway-id=" + id
	bindings, code, err := w.kubernetes.Request(ctx, "GET", "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings?limit=64&labelSelector="+url.QueryEscape(selector), nil)
	entries, ok := kube.Nested(bindings, "items").([]any)
	if err != nil || code != 200 || !ok || len(entries) != 0 || kube.String(bindings, "metadata", "continue") != "" {
		w.t.Fatal("Gateway cluster bindings remained after cleanup", code)
	}
	if response = w.owner.api(w.t, "GET", "/gateways/"+id, nil); response.StatusCode != 404 {
		w.t.Fatal("deleted Gateway remained readable", response.StatusCode)
	}
	w.t.Log("REST Gateway deletion removed both allocated namespaces and completed identity, workload, and database cleanup")
}
