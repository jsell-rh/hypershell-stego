package acceptance

import (
	"errors"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"testing"
)

func TestAllocatedWorkloadPodEvidence(t *testing.T) {
	for _, token := range []bool{false, true} {
		for _, mode := range []string{"ready", "terminating overlap", "empty", "continued", "no version", "wrong kind", "too many", "invalid item", "wrong namespace", "missing uid", "wrong account", "wrong token", "missing token", "pending", "unready", "only terminating", "duplicate ready", "invalid condition", "missing conditions", "invalid conditions", "unknown readiness", "invalid readiness", "duplicate condition", "failed", "unready wrong account", "unready then wrong account", "terminating wrong account"} {
			t.Run(mode+map[bool]string{false: "/console", true: "/gateway"}[token], func(t *testing.T) {
				pod := map[string]any{"metadata": map[string]any{"namespace": "owned", "uid": "pod-uid"}, "spec": map[string]any{"serviceAccountName": "allocated", "automountServiceAccountToken": token}, "status": map[string]any{"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True"}}}}
				page := kube.Object{"apiVersion": "v1", "kind": "PodList", "metadata": map[string]any{"resourceVersion": "1"}, "items": []any{pod}}
				switch mode {
				case "empty":
					page["items"] = []any{}
				case "continued":
					page["metadata"].(map[string]any)["continue"] = "next"
				case "no version":
					delete(page["metadata"].(map[string]any), "resourceVersion")
				case "wrong kind":
					page["kind"] = "List"
				case "too many":
					page["items"] = []any{pod, pod, pod, pod, pod}
				case "invalid item":
					page["items"] = []any{nil}
				case "wrong namespace":
					pod["metadata"].(map[string]any)["namespace"] = "foreign"
				case "missing uid":
					delete(pod["metadata"].(map[string]any), "uid")
				case "wrong account":
					pod["spec"].(map[string]any)["serviceAccountName"] = "default"
				case "wrong token":
					pod["spec"].(map[string]any)["automountServiceAccountToken"] = !token
				case "missing token":
					delete(pod["spec"].(map[string]any), "automountServiceAccountToken")
				case "unready wrong account":
					pod["spec"].(map[string]any)["serviceAccountName"] = "default"
					fallthrough
				case "pending":
					pod["status"].(map[string]any)["phase"] = "Pending"
				case "failed":
					pod["status"].(map[string]any)["phase"] = "Failed"
				case "missing conditions":
					delete(pod["status"].(map[string]any), "conditions")
				case "invalid conditions":
					pod["status"].(map[string]any)["conditions"] = "invalid"
				case "unknown readiness":
					pod["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "Unknown"}}
				case "invalid readiness":
					pod["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": true}}
				case "duplicate condition":
					pod["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "True"}, map[string]any{"type": "Ready", "status": "False"}}
				case "unready":
					pod["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
				case "only terminating":
					pod["metadata"].(map[string]any)["deletionTimestamp"] = "2026-09-17T00:00:00Z"
				case "duplicate ready":
					page["items"] = []any{pod, pod}
				case "invalid condition":
					pod["status"].(map[string]any)["conditions"] = []any{nil}
				case "terminating overlap", "terminating wrong account", "unready then wrong account":
					old := map[string]any{"metadata": map[string]any{"namespace": "owned", "uid": "old-pod", "deletionTimestamp": "2026-09-17T00:00:00Z"}, "spec": map[string]any{"serviceAccountName": "allocated", "automountServiceAccountToken": token}}
					page["items"] = []any{pod, old}
					if mode != "terminating overlap" {
						old["spec"].(map[string]any)["serviceAccountName"] = "default"
					}
					if mode == "unready then wrong account" {
						pod["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
					}
				}
				uid, err := allocatedWorkloadPodIdentity(page, "owned", "allocated", token)
				pending := mode == "empty" || mode == "pending" || mode == "unready" || mode == "only terminating" || mode == "missing conditions" || mode == "unknown readiness"
				if errors.Is(err, errAllocatedPodPending) != pending {
					t.Fatal("Pod readiness wait classification differs", err)
				}
				want := mode == "ready" || mode == "terminating overlap"
				if want && (err != nil || uid != "pod-uid") {
					t.Fatal("ready Pod was rejected", uid, err)
				}
				if !want && (err == nil || uid != "") {
					t.Fatal("invalid Pod evidence was accepted", uid, err)
				}
			})
		}
	}
}
