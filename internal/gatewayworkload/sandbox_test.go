package gatewayworkload

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"
)

func TestSandboxDeletionRetainsAdmissionUntilNamespaceIsAbsent(t *testing.T) {
	gw, _, _ := records(t)
	ns, _ := SandboxNamespace(gw.Metadata.Id)
	for _, state := range []string{"live", "terminating", "foreign"} {
		t.Run(state, func(t *testing.T) {
			deleted := false
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/namespaces/"+ns {
					t.Error("cleanup reached admission before namespace was absent", r.URL.Path)
					w.WriteHeader(500)
					return
				}
				obj := definition("v1", "Namespace", ns, gw.Metadata.Id)
				meta := obj["metadata"].(object)
				meta["uid"] = "sandbox-uid"
				meta["resourceVersion"] = "7"
				if state == "terminating" {
					meta["deletionTimestamp"] = "2026-09-09T00:00:00Z"
				}
				if state == "foreign" {
					meta["labels"].(object)[ownerLabel] = "other"
				}
				if r.Method == http.MethodDelete {
					deleted = true
				}
				_ = json.NewEncoder(w).Encode(obj)
			})
			err := k.Delete(context.Background(), gw)
			if err == nil {
				t.Fatal("cleanup finished while sandbox namespace existed")
			}
			if state != "foreign" && !errors.Is(err, ErrPending) {
				t.Fatal(err)
			}
			if deleted != (state == "live") {
				t.Fatal("unsafe namespace deletion", state)
			}
		})
	}
}
