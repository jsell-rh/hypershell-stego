package gatewayworkload

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func TestWorkloadReadinessUsesOwnedAvailableDeployment(t *testing.T) {
	for _, test := range []struct {
		name             string
		code             int
		change           func(object)
		pending, invalid bool
	}{
		{name: "available", code: 200},
		{name: "ready before available", code: 200, change: func(o object) { delete(o["status"].(object), "availableReplicas") }, pending: true},
		{name: "old rollout", code: 200, change: func(o object) { o["status"].(object)["observedGeneration"] = 1 }, pending: true},
		{name: "old replica remains", code: 200, change: func(o object) { o["status"].(object)["replicas"] = 2 }, pending: true},
		{name: "foreign owner", code: 200, change: func(o object) { o["metadata"].(object)["labels"] = object{ownerLabel: "other", managerLabel: manager} }, invalid: true},
		{name: "deleting", code: 200, change: func(o object) { o["metadata"].(object)["deletionTimestamp"] = "2026-09-15T00:00:00Z" }, pending: true},
		{name: "missing", code: 404, pending: true},
		{name: "denied", code: 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			gw, _ := records(t)
			current := object{"apiVersion": "apps/v1", "kind": "Deployment", "metadata": object{"name": Name, "namespace": gw.Namespace, "uid": "deployment-1", "resourceVersion": "3", "generation": 2, "labels": object{ownerLabel: gw.Metadata.Id, managerLabel: manager}}, "spec": object{"replicas": 1}, "status": object{"observedGeneration": 2, "replicas": 1, "updatedReplicas": 1, "readyReplicas": 1, "availableReplicas": 1, "conditions": []object{{"type": "Available", "status": "True"}, {"type": "Progressing", "status": "True"}}}}
			if test.change != nil {
				test.change(current)
			}
			calls := 0
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/apis/apps/v1/namespaces/"+gw.Namespace+"/deployments/"+Name || r.Header.Get("Authorization") != "Bearer acceptance-token" {
					t.Error("unexpected workload observation request")
					w.WriteHeader(500)
					return
				}
				w.WriteHeader(test.code)
				if test.code == 200 {
					_ = json.NewEncoder(w).Encode(current)
				}
			})
			err := k.deploymentAvailable(context.Background(), gw.Metadata.Id, gw.Namespace)
			switch {
			case test.pending:
				if !errors.Is(err, ErrPending) {
					t.Fatal("incomplete workload was not pending", err)
				}
			case test.invalid:
				if !errors.Is(err, kube.ErrResourceObservation) {
					t.Fatal("foreign observation was accepted", err)
				}
			case test.code == 403:
				var failure *kube.APIError
				if !errors.As(err, &failure) || failure.StatusCode != 403 {
					t.Fatal("denied observation was hidden", err)
				}
			default:
				if err != nil {
					t.Fatal(err)
				}
			}
			if calls != 1 {
				t.Fatal("readiness made extra requests", calls)
			}
		})
	}
}
