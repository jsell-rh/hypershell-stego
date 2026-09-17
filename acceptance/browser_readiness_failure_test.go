package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

type readinessPod struct {
	Component          string `json:"component"`
	Phase              string `json:"phase"`
	Ready              bool   `json:"ready"`
	Scheduled          bool   `json:"scheduled"`
	InsufficientCPU    bool   `json:"insufficient_cpu"`
	InsufficientMemory bool   `json:"insufficient_memory"`
	ImagePullFailure   bool   `json:"image_pull_failure"`
	CrashLoop          bool   `json:"crash_loop"`
}

// Keep only fixed categories. Pod settings and server messages can contain
// private data and must not enter the failure artifact.
func readinessPodSummaries(list kube.Object, namespace string) ([]readinessPod, bool) {
	items, ok := list["items"].([]any)
	if !ok || len(items) > 32 || kube.String(list, "metadata", "continue") != "" {
		return nil, false
	}
	result := []readinessPod{}
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok {
			return nil, false
		}
		pod := kube.Object(raw)
		if kube.String(pod, "metadata", "namespace") != namespace {
			return nil, false
		}
		component := ""
		switch kube.String(pod, "metadata", "labels", "app.kubernetes.io/name") {
		case "hypershell-gateway-console":
			component = "console"
		case "openshell-gateway":
			component = "gateway"
		}
		if component == "" {
			continue
		}
		summary := readinessPod{Component: component, Phase: "unknown"}
		switch phase := kube.String(pod, "status", "phase"); phase {
		case "Pending", "Running", "Succeeded", "Failed", "Unknown":
			summary.Phase = phase
		}
		conditions, _ := kube.Nested(pod, "status", "conditions").([]any)
		for _, item := range conditions {
			raw, ok := item.(map[string]any)
			if !ok {
				return nil, false
			}
			condition := kube.Object(raw)
			switch kube.String(condition, "type") {
			case "Ready":
				summary.Ready = kube.String(condition, "status") == "True"
			case "PodScheduled":
				summary.Scheduled = kube.String(condition, "status") == "True"
				if kube.String(condition, "status") == "False" && kube.String(condition, "reason") == "Unschedulable" {
					message := kube.String(condition, "message")
					summary.InsufficientCPU = strings.Contains(message, "Insufficient cpu")
					summary.InsufficientMemory = strings.Contains(message, "Insufficient memory")
				}
			}
		}
		for _, field := range []string{"containerStatuses", "initContainerStatuses"} {
			statuses, _ := kube.Nested(pod, "status", field).([]any)
			for _, item := range statuses {
				raw, ok := item.(map[string]any)
				if !ok {
					return nil, false
				}
				status := kube.Object(raw)
				switch kube.String(status, "state", "waiting", "reason") {
				case "ImagePullBackOff", "ErrImagePull":
					summary.ImagePullFailure = true
				case "CrashLoopBackOff":
					summary.CrashLoop = true
				}
			}
		}
		result = append(result, summary)
	}
	return result, true
}

func (w *browserGatewayWorkload) recordReadinessFailure(id string) {
	w.t.Helper()
	// The namespace comes from this Gateway's fixed placement, not response text.
	namespace, err := gatewayworkload.Namespace(id)
	if err != nil {
		w.t.Log("Readiness failure namespace is invalid")
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	list, code, err := w.kubernetes.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+namespace+"/pods?limit=32", nil)
	pods, complete := readinessPodSummaries(list, namespace)
	complete = complete && err == nil && code == http.StatusOK
	record := struct {
		Gateway  string         `json:"gateway_id"`
		Complete bool           `json:"read_complete"`
		Pods     []readinessPod `json:"pods"`
	}{id, complete, pods}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		w.t.Log("Readiness failure evidence encoding failed")
		return
	}
	directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR")
	if directory != "" && os.WriteFile(filepath.Join(directory, "gateway-readiness-failure.json"), append(data, '\n'), 0600) != nil {
		w.t.Log("Readiness failure evidence write failed")
	}
}

func TestReadinessFailureEvidenceExcludesPrivatePodFields(t *testing.T) {
	raw := []byte(`{"items":[{"metadata":{"namespace":"gateway-test","name":"private-name","labels":{"app.kubernetes.io/name":"hypershell-gateway-console"}},"spec":{"containers":[{"env":[{"name":"PASSWORD","value":"private-credential"}]}]},"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False","reason":"Unschedulable","message":"private-node: Insufficient cpu, Insufficient memory"}],"containerStatuses":[{"state":{"waiting":{"reason":"ImagePullBackOff","message":"private-registry"}}}]}}]}`)
	var list kube.Object
	if err := json.Unmarshal(raw, &list); err != nil {
		t.Fatal(err)
	}
	pods, ok := readinessPodSummaries(list, "gateway-test")
	if !ok || len(pods) != 1 || !pods[0].InsufficientCPU || !pods[0].InsufficientMemory || !pods[0].ImagePullFailure || pods[0].Scheduled || pods[0].Ready {
		t.Fatal("scheduling failure was not preserved")
	}
	data, err := json.Marshal(pods)
	if err != nil || strings.Contains(string(data), "private-") || strings.Contains(string(data), "PASSWORD") {
		t.Fatal("private Pod data entered evidence")
	}
	if _, ok := readinessPodSummaries(list, "foreign"); ok {
		t.Fatal("foreign namespace was accepted")
	}
	list["metadata"] = map[string]any{"continue": "next"}
	if _, ok := readinessPodSummaries(list, "gateway-test"); ok {
		t.Fatal("partial list was accepted")
	}
}
