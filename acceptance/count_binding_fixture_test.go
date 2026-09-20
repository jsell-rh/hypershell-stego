package acceptance

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// Select by role and subject. Generated binding order is not an API contract.
func selectCountBinding(page kube.Object, owner kube.Owner, control, namespace string) (kube.Object, error) {
	invalid := errors.New("count fixture requires one complete owned binding identity")
	entries, ok := kube.Nested(page, "items").([]any)
	if !ok || len(entries) > 64 || kube.String(page, "metadata", "resourceVersion") == "" || kube.String(page, "metadata", "continue") != "" {
		return nil, invalid
	}
	want := kube.Object{
		"roleRef":  kube.Object{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": control + ".hypershell-namespace-allocation.sandbox-count"},
		"subjects": []any{kube.Object{"kind": "ServiceAccount", "name": "hypershell-sandbox-count", "namespace": control}},
	}
	var selected kube.Object
	for _, entry := range entries {
		value, ok := entry.(map[string]any)
		if !ok {
			return nil, invalid
		}
		binding := kube.Object(value)
		if kube.String(binding, "roleRef", "name") != control+".hypershell-namespace-allocation.sandbox-count" {
			continue
		}
		name := kube.String(binding, "metadata", "name")
		if selected != nil || !owner.Matches(binding) || !kube.Contains(binding, want) || name == "" || strings.ContainsAny(name, "/%?#") || kube.String(binding, "metadata", "namespace") != namespace || kube.String(binding, "metadata", "uid") == "" || kube.String(binding, "metadata", "resourceVersion") == "" || kube.String(binding, "metadata", "deletionTimestamp") != "" {
			return nil, invalid
		}
		selected = binding
	}
	if selected == nil {
		return nil, invalid
	}
	return selected, nil
}

func TestCountBindingFixtureUsesIdentity(t *testing.T) {
	owner := kube.Owner{"test-owner": "gateway"}
	base := `{"metadata":{"resourceVersion":"9"},"items":[{"metadata":{"name":"stego-marker-5","namespace":"gateway-ns","uid":"uid","resourceVersion":"8","labels":{"test-owner":"gateway"}},"roleRef":{"apiGroup":"rbac.authorization.k8s.io","kind":"ClusterRole","name":"control.hypershell-namespace-allocation.sandbox-count"},"subjects":[{"kind":"ServiceAccount","name":"hypershell-sandbox-count","namespace":"control"}]}]}`
	for _, mode := range []string{"valid", "new-name", "reordered", "missing", "duplicate", "continued", "no-list-version", "wrong-owner", "wrong-namespace", "wrong-subject", "extra-subject", "no-uid", "no-version", "deleting"} {
		t.Run(mode, func(t *testing.T) {
			var page kube.Object
			if err := json.Unmarshal([]byte(base), &page); err != nil {
				t.Fatal(err)
			}
			items := page["items"].([]any)
			binding := items[0].(map[string]any)
			meta := binding["metadata"].(map[string]any)
			switch mode {
			case "new-name":
				meta["name"] = "stego-marker-9"
			case "reordered":
				page["items"] = []any{map[string]any{"roleRef": map[string]any{"name": "unrelated"}}, items[0]}
			case "missing":
				page["items"] = []any{}
			case "duplicate":
				page["items"] = append(items, items[0])
			case "continued":
				page["metadata"].(map[string]any)["continue"] = "next"
			case "no-list-version":
				delete(page["metadata"].(map[string]any), "resourceVersion")
			case "wrong-owner":
				meta["labels"] = map[string]any{"test-owner": "other"}
			case "wrong-namespace":
				meta["namespace"] = "other"
			case "wrong-subject":
				binding["subjects"].([]any)[0].(map[string]any)["namespace"] = "other"
			case "extra-subject":
				binding["subjects"] = append(binding["subjects"].([]any), map[string]any{"kind": "ServiceAccount", "name": "other", "namespace": "control"})
			case "no-uid":
				delete(meta, "uid")
			case "no-version":
				delete(meta, "resourceVersion")
			case "deleting":
				meta["deletionTimestamp"] = "2026-09-20T00:00:00Z"
			}
			selected, err := selectCountBinding(page, owner, "control", "gateway-ns")
			valid := mode == "valid" || mode == "new-name" || mode == "reordered"
			if valid && (err != nil || kube.String(selected, "metadata", "name") != meta["name"]) {
				t.Fatal("owned binding was not selected", err)
			}
			if !valid && (err == nil || selected != nil) {
				t.Fatal("invalid binding snapshot was accepted")
			}
		})
	}
}
