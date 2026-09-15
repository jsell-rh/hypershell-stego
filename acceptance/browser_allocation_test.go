package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

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
	state, gateway := "", ""
	for ns, target := range w.allocations {
		if target.profile == "gateway-state" {
			state = ns
		} else {
			gateway = ns
		}
		if err := allocator.RequireNamespace(ctx, target.profile, ns, target.id); err != nil {
			w.t.Fatal("namespace was not allocated", err)
		}
		pods, storage := "2", "512Mi"
		if target.profile == "gateway-state" {
			pods, storage = "0", "64Mi"
		}
		quota, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+ns+"/resourcequotas/stego-allocation", nil)
		if err != nil || code != 200 || kube.String(quota, "spec", "hard", "pods") != pods || kube.String(quota, "spec", "hard", "limits.ephemeral-storage") != storage {
			w.t.Fatal("allocation limits missing", code)
		}
	}
	if state == "" || gateway == "" {
		w.t.Fatal("allocation targets missing")
	}
	type check struct {
		Worker, Namespace, Group, Resource, Verb string
		Allowed                                  bool
	}
	checks := []check{
		{"gateway-workload", gateway, "apps", "deployments", "create", true},
		{"gateway-workload", state, "", "pods", "create", false},
		{"gateway-workload", state, "", "persistentvolumeclaims", "create", false},
		{"gateway-workload", state, "postgresql.cnpg.io", "clusters", "create", false},
		{"gateway-workload", w.p.namespace, "postgresql.cnpg.io", "clusters", "delete", false},
		{"gateway-workload", state, "", "secrets", "get", true},
		{"gateway-workload", w.p.namespace, "", "secrets", "get", false},
		{"gateway-workload", "", "", "namespaces", "delete", false},
		{"gateway-workload", "", "rbac.authorization.k8s.io", "clusterroles", "create", false},
		{"gateway-workload", gateway, "rbac.authorization.k8s.io", "rolebindings", "create", false},
		// Public state fingerprints require patch. Admission keeps them immutable.
		{"namespace-allocation", "", "", "namespaces", "patch", true},
		{"namespace-allocation", state, "", "secrets", "get", false},
		{"namespace-allocation", gateway, "", "secrets", "get", false},
		{"namespace-allocation", w.p.namespace, "", "secrets", "get", false},
		{"gateway-identity", state, "", "secrets", "get", false},
	}
	clients := map[string]*kube.Client{}
	allocatorToken := ""
	for _, test := range checks {
		client := clients[test.Worker]
		if client == nil {
			token, code, err := w.kubernetes.Request(ctx, "POST", "/api/v1/namespaces/"+w.p.namespace+"/serviceaccounts/hypershell-"+test.Worker+"/token", kube.Object{"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest", "spec": kube.Object{"expirationSeconds": 600}})
			if err != nil || code != 201 {
				w.t.Fatal("test identity request failed", code)
			}
			value := kube.String(token, "status", "token")
			if test.Worker == "namespace-allocation" {
				allocatorToken = value
			}
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
	w.checkInstallationAccess()
	w.checkAdmission(ctx, state, allocatorToken)
	if dir := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); dir != "" {
		data, err := json.MarshalIndent(checks, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(dir, "allocation-permissions.json"), data, 0600) != nil {
			w.t.Fatal("cannot write allocation evidence")
		}
	}
	w.t.Log("Allocated namespaces have fixed limits; live worker access checks passed")
}

// A normal REST deletion must drive allocation cleanup and controller records.
func (w *browserGatewayWorkload) checkAllocatedDeletion(id string) {
	w.t.Helper()
	response := w.owner.api(w.t, "GET", "/gateways/"+id, nil)
	var gateway httpapi.Gateway
	if response.StatusCode != 200 || json.Unmarshal(response.Body, &gateway) != nil {
		w.t.Fatal("Gateway deletion setup failed")
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
	w.awaitGatewayCleanup(ctx, allocator, id)
	if response = w.owner.api(w.t, "GET", "/gateways/"+id, nil); response.StatusCode != 404 {
		w.t.Fatal("deleted Gateway remained readable", response.StatusCode)
	}
	w.checkSQLDeletion(id)
	for _, other := range w.gatewayIDs {
		if other != id {
			w.check(other)
		}
	}
	w.t.Log("REST Gateway deletion removed its namespace, SQL state, role, and keys; the other Gateway and supplied PostgreSQL server remained available")
}
