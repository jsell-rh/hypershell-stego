package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
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
		} else if target.profile == "gateway" {
			gateway = ns
		}
		if err := allocator.RequireNamespace(ctx, target.profile, ns, target.id); err != nil {
			w.t.Fatal("namespace allocation verification failed", target.profile, err)
		}
		pods, storage := "3", "768Mi"
		if target.profile == "sandbox" {
			pods, storage = "32", "16Gi"
		} else if target.profile == "gateway-state" || target.profile == "gateway-console-state" {
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
		{"gateway-workload", gateway, "", "serviceaccounts", "get", true},
		{"gateway-workload", gateway, "", "serviceaccounts", "create", false},
		{"gateway-workload", gateway, "", "serviceaccounts", "patch", false},
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
	for _, id := range w.gatewayIDs {
		namespace, err := gatewayworkload.SandboxNamespace(id)
		if err != nil {
			w.t.Fatal(err)
		}
		checks = append(checks,
			check{"gateway-workload", namespace, "", "pods", "create", true},
			check{"gateway-workload", namespace, "", "secrets", "create", true},
			check{"gateway-workload", namespace, "", "serviceaccounts", "get", true},
			check{"gateway-workload", namespace, "", "serviceaccounts", "create", false},
			check{"gateway-workload", namespace, "", "serviceaccounts", "patch", false},
			check{"gateway-workload", namespace, "networking.k8s.io", "networkpolicies", "patch", false},
			check{"gateway-identity", namespace, "", "secrets", "get", false},
			check{"namespace-allocation", namespace, "", "secrets", "get", false},
		)
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
	// Submit real forbidden requests as the workload worker. Dry run ensures
	// that an unexpected grant cannot leave an account or policy change behind.
	type denial struct {
		Namespace, Method, Path string
		Code                    int
	}
	var denials []denial
	for _, id := range w.gatewayIDs {
		namespace, err := gatewayworkload.SandboxNamespace(id)
		if err != nil {
			w.t.Fatal(err)
		}
		for _, probe := range []struct {
			method, path string
			body         kube.Object
		}{
			{"POST", "/api/v1/namespaces/" + namespace + "/serviceaccounts?dryRun=All", kube.Object{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": kube.Object{"namespace": namespace, "name": "stego-denied-account"}}},
			{"PATCH", "/apis/networking.k8s.io/v1/namespaces/" + namespace + "/networkpolicies/stego-allocation?dryRun=All", kube.Object{"spec": kube.Object{"egress": []any{kube.Object{}}}}},
		} {
			_, code, err := clients["gateway-workload"].Request(ctx, probe.method, probe.path, probe.body)
			if code != 403 || err == nil {
				w.t.Fatal("Sandbox worker request was not forbidden", probe.method, code)
			}
			denials = append(denials, denial{namespace, probe.method, probe.path, code})
		}
	}
	w.checkInstallationAccess(state, gateway)
	w.checkAdmission(ctx, state, gateway, allocator.Marker(), allocatorToken)
	if dir := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); dir != "" {
		data, err := json.MarshalIndent(checks, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(dir, "allocation-permissions.json"), data, 0600) != nil {
			w.t.Fatal("cannot write allocation evidence")
		}
		data, err = json.MarshalIndent(denials, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(dir, "allocation-sandbox-denials.json"), data, 0600) != nil {
			w.t.Fatal("cannot write Sandbox denial evidence")
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
	checkAccounts := w.startAccountDeletionWorkflow(id)
	checkDeniedCleanup := w.beginSQLCleanupDenial(id)
	response = w.owner.api(w.t, "DELETE", "/gateways/"+id, nil)
	if response.StatusCode != 202 {
		w.t.Fatal("Gateway deletion failed", response.StatusCode)
	}
	checkAccounts()
	resumeAllocation := checkDeniedCleanup()
	defer resumeAllocation()
	completeAllocationProof := w.checkPendingAllocationCleanup(id)
	resumeAllocation()
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
	completeAllocationProof()
	for _, other := range w.gatewayIDs {
		if other != id {
			w.check(other)
		}
	}
	w.t.Log("REST Gateway deletion removed its namespace, SQL state, role, and keys; the other Gateway and supplied PostgreSQL server remained available")
}
