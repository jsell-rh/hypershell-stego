package acceptance

import (
	"context"
	"encoding/json"
	"fmt"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

type databaseRestartPodReader interface {
	Request(context.Context, string, string, kube.Object) (kube.Object, int, error)
}

// Report only fixed failure categories and the HTTP status. The response body,
// request path, token, and provider error text must not enter test diagnostics.
// Keep the existing identity checks and caller deadline. Do not retry reads.
func readDatabaseRestartPod(ctx context.Context, client databaseRestartPodReader, namespace, pod string) (string, int, bool, error) {
	object, code, err := client.Request(ctx, "GET", "/api/v1/namespaces/"+namespace+"/pods/"+pod, nil)
	failure := func(category string) (string, int, bool, error) {
		return "", 0, false, fmt.Errorf("database restart requires this bounded fixture Pod: category=%s status=%d", category, code)
	}
	if err != nil {
		switch ctx.Err() {
		case context.DeadlineExceeded:
			return failure("deadline")
		case context.Canceled:
			return failure("canceled")
		}
		if code != 0 {
			return failure("http_status")
		}
		return failure("request_failed")
	}
	if code != 200 {
		return failure("http_status")
	}
	data, err := json.Marshal(object)
	if err != nil {
		return failure("object_encoding")
	}
	var state struct {
		Metadata struct {
			UID    string
			Labels map[string]string
		}
		Status struct {
			InitContainerStatuses []struct {
				Name         string
				RestartCount int
				Ready        bool
			}
		}
	}
	if json.Unmarshal(data, &state) != nil {
		return failure("object_decoding")
	}
	if state.Metadata.UID == "" {
		return failure("pod_identity")
	}
	if state.Metadata.Labels["app"] != "stego-fixture" {
		return failure("app_label")
	}
	if state.Metadata.Labels["job-name"] != "service-check" {
		return failure("job_label")
	}
	for _, container := range state.Status.InitContainerStatuses {
		if container.Name == "postgres" {
			return state.Metadata.UID, container.RestartCount, container.Ready, nil
		}
	}
	return failure("postgres_status_missing")
}
