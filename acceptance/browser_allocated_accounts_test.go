package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// Check the common account contract each time the real Gateway becomes ready.
// The workflow repeats this check after worker and namespace replacement.
func (w *browserGatewayWorkload) checkAllocatedWorkloadAccounts(id, namespace string) bool {
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
	pending := false
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
		selector, ok := kube.Nested(deployment, "spec", "selector", "matchLabels").(map[string]any)
		if !ok || len(selector) == 0 || len(selector) > 8 {
			w.t.Fatal("allocated workload selector is unavailable", target.alias)
		}
		labels := make([]string, 0, len(selector))
		for key, value := range selector {
			label, ok := value.(string)
			if !ok || key == "" || label == "" {
				w.t.Fatal("allocated workload selector is invalid", target.alias)
			}
			labels = append(labels, key+"="+label)
		}
		sort.Strings(labels)
		pods, code, err := w.kubernetes.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+namespace+"/pods?limit=4&labelSelector="+url.QueryEscape(strings.Join(labels, ",")), nil)
		if err != nil || code != http.StatusOK {
			w.t.Fatal("allocated workload Pod read failed", target.alias)
		}
		podUID, err := allocatedWorkloadPodIdentity(pods, namespace, name, target.token)
		if errors.Is(err, errAllocatedPodPending) {
			pending = true
			continue
		}
		if err != nil {
			w.t.Fatal("allocated workload Pod differs", target.alias, err)
		}
		records[target.alias] = map[string]any{"name": name, "uid": kube.String(account, "metadata", "uid"), "deployment_uid": kube.String(deployment, "metadata", "uid"), "pod_uid": podUID, "pod_token_mount": target.token, "account_token_mount": false}
	}
	if pending {
		return false
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
	return true
}

// A valid Pod can need more time after an earlier readiness observation.
var errAllocatedPodPending = errors.New("current Pod is not ready")

// Inspect every identity before reporting pending readiness. A terminating Pod
// must use the required account and token setting, but cannot prove readiness.
func allocatedWorkloadPodIdentity(page kube.Object, namespace, account string, token bool) (string, error) {
	items, ok := page["items"].([]any)
	if !ok || len(items) > 4 || kube.String(page, "apiVersion") != "v1" || kube.String(page, "kind") != "PodList" || kube.String(page, "metadata", "resourceVersion") == "" || kube.String(page, "metadata", "continue") != "" {
		return "", errors.New("Pod inventory is incomplete")
	}
	uid := ""
	pending := false
	for _, value := range items {
		pod, ok := value.(map[string]any)
		if !ok || kube.String(pod, "metadata", "namespace") != namespace || kube.String(pod, "metadata", "uid") == "" {
			return "", errors.New("Pod identity is invalid")
		}
		if kube.String(pod, "spec", "serviceAccountName") != account || kube.Nested(pod, "spec", "automountServiceAccountToken") != token {
			return "", errors.New("Pod account or token setting differs")
		}
		if kube.String(pod, "metadata", "deletionTimestamp") != "" {
			continue
		}
		if uid != "" {
			return "", errors.New("multiple current Pods cannot prove readiness")
		}
		uid = kube.String(pod, "metadata", "uid")
		switch kube.String(pod, "status", "phase") {
		case "Running":
		case "Pending":
			pending = true
		default:
			return "", errors.New("Pod phase is invalid")
		}
		raw := kube.Nested(pod, "status", "conditions")
		conditions, ok := raw.([]any)
		if raw != nil && !ok {
			return "", errors.New("Pod readiness is invalid")
		}
		readyCount := 0
		ready := false
		for _, value := range conditions {
			condition, valid := value.(map[string]any)
			if !valid {
				return "", errors.New("Pod readiness is invalid")
			}
			if condition["type"] == "Ready" {
				readyCount++
				switch condition["status"] {
				case "True":
					ready = true
				case "False", "Unknown":
				default:
					return "", errors.New("Pod readiness status is invalid")
				}
			}
		}
		if readyCount > 1 {
			return "", errors.New("Pod readiness is ambiguous")
		}
		pending = pending || !ready
	}
	if uid == "" || pending {
		return "", errAllocatedPodPending
	}
	return uid, nil
}
