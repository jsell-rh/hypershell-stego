package acceptance

import (
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"testing"
)

func TestAllocatedWorkloadPodEvidence(t *testing.T) {
	for _, token := range []bool{false, true} {
		for _, mode := range []string{"ready", "terminating overlap", "empty", "continued", "no version", "wrong kind", "too many", "invalid item", "wrong namespace", "missing uid", "wrong account", "wrong token", "missing token", "pending", "unready", "only terminating", "duplicate ready", "invalid condition"} {
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
				case "pending":
					pod["status"].(map[string]any)["phase"] = "Pending"
				case "unready":
					pod["status"].(map[string]any)["conditions"] = []any{map[string]any{"type": "Ready", "status": "False"}}
				case "only terminating":
					pod["metadata"].(map[string]any)["deletionTimestamp"] = "2026-09-17T00:00:00Z"
				case "duplicate ready":
					page["items"] = []any{pod, pod}
				case "invalid condition":
					pod["status"].(map[string]any)["conditions"] = []any{nil}
				case "terminating overlap":
					page["items"] = []any{map[string]any{"metadata": map[string]any{"namespace": "owned", "uid": "old-pod", "deletionTimestamp": "2026-09-17T00:00:00Z"}}, pod}
				}
				uid, err := allocatedWorkloadPodIdentity(page, "owned", "allocated", token)
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
