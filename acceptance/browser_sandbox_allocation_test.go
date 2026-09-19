package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

type sandboxAllocationRecord struct {
	GatewayID, Namespace, NamespaceUID, Account, AccountUID, GatewayNamespace, GatewayAccount string
}

// The real allocation controller creates this boundary from recorded Gateway
// placement. This check performs no allocation writes and starts no Sandbox Pod.
func (w *browserGatewayWorkload) checkSandboxAllocations(stage string) {
	w.t.Helper()
	if len(w.gatewayIDs) != 2 || (stage != "initial" && stage != "after-recovery") {
		w.t.Fatal("Sandbox allocation check requires two Gateways and a known stage")
	}
	allocator, err := allocation.New(w.kubernetes, w.p.namespace)
	if err != nil {
		w.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	records := map[string]sandboxAllocationRecord{}
	for _, id := range w.gatewayIDs {
		namespace, err := gatewayworkload.SandboxNamespace(id)
		if err != nil {
			w.t.Fatal(err)
		}
		gateway, err := gatewayworkload.Namespace(id)
		if err != nil || gateway == namespace {
			w.t.Fatal("Sandbox placement is not separate from its Gateway")
		}
		var account string
		for {
			err = allocator.RequireNamespace(ctx, "sandbox", namespace, id)
			if err == nil {
				account, err = allocator.RequireServiceAccount(ctx, "sandbox", namespace, id, "sandbox")
			}
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				w.t.Fatal("controller did not complete the Sandbox allocation", err)
			}
			time.Sleep(250 * time.Millisecond)
		}
		uid, err := allocator.NamespaceUID(ctx, "sandbox", namespace, id)
		if err != nil {
			w.t.Fatal(err)
		}
		core := "/api/v1/namespaces/" + namespace
		actual, code, err := w.kubernetes.Request(ctx, http.MethodGet, core+"/serviceaccounts/"+account, nil)
		accountUID := kube.String(actual, "metadata", "uid")
		if err != nil || code != http.StatusOK || accountUID == "" || actual["automountServiceAccountToken"] != false {
			w.t.Fatal("Sandbox account identity or token setting differs", code)
		}
		gatewayAccount, err := allocator.RequireServiceAccount(ctx, "gateway", gateway, id, "gateway")
		if err != nil {
			w.t.Fatal(err)
		}
		selector := url.QueryEscape(allocation.MarkerLabel + "=" + allocator.Marker())
		bindings, code, err := w.kubernetes.Request(ctx, http.MethodGet, "/apis/rbac.authorization.k8s.io/v1/namespaces/"+namespace+"/rolebindings?limit=16&labelSelector="+selector, nil)
		items, ok := bindings["items"].([]any)
		if err != nil || code != http.StatusOK || !ok || len(items) > 16 || kube.String(bindings, "metadata", "continue") != "" || kube.String(bindings, "metadata", "resourceVersion") == "" {
			w.t.Fatal("Sandbox binding inventory is incomplete", code)
		}
		matched := 0
		for _, value := range items {
			binding, ok := value.(map[string]any)
			if !ok {
				w.t.Fatal("Sandbox binding is invalid")
			}
			if kube.String(binding, "roleRef", "name") != w.p.namespace+".hypershell-namespace-allocation.gateway-runtime" {
				continue
			}
			matched++
			subjects, ok := binding["subjects"].([]any)
			if !ok || len(subjects) != 1 {
				w.t.Fatal("Sandbox Gateway binding has unexpected subjects")
			}
			subject, ok := subjects[0].(map[string]any)
			if !ok || subject["kind"] != "ServiceAccount" || subject["namespace"] != gateway || subject["name"] != gatewayAccount || kube.String(binding, "metadata", "uid") == "" {
				w.t.Fatal("Sandbox access is not bound to its assigned Gateway account")
			}
		}
		if matched != 1 {
			w.t.Fatal("Sandbox has no unique Gateway runtime binding", matched)
		}
		pods, code, err := w.kubernetes.Request(ctx, http.MethodGet, core+"/pods?limit=1", nil)
		entries, ok := pods["items"].([]any)
		if err != nil || code != http.StatusOK || !ok || len(entries) != 0 || kube.String(pods, "metadata", "continue") != "" {
			w.t.Fatal("allocation-only check found a Sandbox Pod", code)
		}
		records[id] = sandboxAllocationRecord{id, namespace, uid, account, accountUID, gateway, gatewayAccount}
	}
	if stage == "initial" {
		if w.sandboxAllocations != nil {
			w.t.Fatal("Sandbox allocation baseline was already recorded")
		}
		w.sandboxAllocations = records
	} else if !reflect.DeepEqual(w.sandboxAllocations, records) {
		w.t.Fatal("worker or Gateway namespace recovery changed the Sandbox allocation")
	}
	if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
		data, err := json.MarshalIndent(map[string]any{"stage": stage, "allocations": records, "sandbox_pods_observed": 0, "network_traffic_checked": false, "kata_execution_checked": false}, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(directory, "sandbox-allocation-"+stage+".json"), append(data, '\n'), 0600) != nil {
			w.t.Fatal("cannot save Sandbox allocation evidence")
		}
	}
	w.t.Log("Generated Sandbox accounts and Gateway bindings passed; no Sandbox Pod remains")
}
