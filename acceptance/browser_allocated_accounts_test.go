package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// Check the common account contract each time the real Gateway becomes ready.
// The workflow repeats this check after worker and namespace replacement.
func (w *browserGatewayWorkload) checkAllocatedWorkloadAccounts(id, namespace string) {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	allocator, err := allocation.New(w.kubernetes, w.p.namespace)
	if err != nil {
		w.t.Fatal(err)
	}
	namespaceUID, err := allocator.NamespaceUID(ctx, "gateway", namespace, id)
	if err != nil {
		w.t.Fatal("workload namespace identity is unavailable", err)
	}
	records := map[string]any{}
	names := map[string]bool{}
	for _, target := range []struct {
		alias, deployment string
		token             bool
	}{{"gateway", "openshell-gateway", true}, {"console", "hypershell-gateway-console", false}} {
		name, err := allocator.RequireServiceAccount(ctx, "gateway", namespace, id, target.alias)
		if err != nil || names[name] {
			w.t.Fatal("workload account is invalid or shared", target.alias, err)
		}
		names[name] = true
		account, code, err := w.kubernetes.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+namespace+"/serviceaccounts/"+name, nil)
		if err != nil || code != http.StatusOK || kube.String(account, "metadata", "uid") == "" || account["automountServiceAccountToken"] != false {
			w.t.Fatal("workload account identity is unavailable", target.alias)
		}
		deployment, code, err := w.kubernetes.Request(ctx, http.MethodGet, "/apis/apps/v1/namespaces/"+namespace+"/deployments/"+target.deployment, nil)
		if err != nil || code != http.StatusOK || kube.String(deployment, "metadata", "uid") == "" {
			w.t.Fatal("allocated workload deployment is unavailable", target.alias)
		}
		if kube.String(deployment, "spec", "template", "spec", "serviceAccountName") != name || kube.Nested(deployment, "spec", "template", "spec", "automountServiceAccountToken") != target.token {
			w.t.Fatal("workload account or token mount differs", target.alias)
		}
		records[target.alias] = map[string]any{"name": name, "uid": kube.String(account, "metadata", "uid"), "deployment_uid": kube.String(deployment, "metadata", "uid"), "pod_token_mount": target.token, "account_token_mount": false}
	}
	if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
		data, err := json.Marshal(map[string]any{"gateway_id": id, "namespace": namespace, "namespace_uid": namespaceUID, "accounts": records})
		if err != nil {
			w.t.Fatal(err)
		}
		file, err := os.OpenFile(filepath.Join(directory, "allocated-workload-accounts.jsonl"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
		if err != nil {
			w.t.Fatal(err)
		}
		_, writeErr := file.Write(append(data, '\n'))
		closeErr := file.Close()
		if writeErr != nil || closeErr != nil {
			w.t.Fatal("cannot save workload account evidence")
		}
	}
	w.t.Log("Gateway and console use separate allocated accounts with explicit Pod token settings")
}
