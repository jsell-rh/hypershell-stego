package databasecontroller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
)

func TestAllocatedCNPGOnlyObservesNamespaceCleanup(t *testing.T) {
	db := cnpgRow()
	id, ns := db.Metadata.Id, db.Namespace
	for _, phase := range []string{"active", "deleting", "absent", "foreign", "wrong-profile", "wrong-owner", "denied"} {
		t.Run(phase, func(t *testing.T) {
			var allocator *allocation.Allocator
			k, _ := testKubernetes(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "GET" && r.URL.Path == cnpgAPI {
					_ = json.NewEncoder(w).Encode(cnpgDiscovery())
					return
				}
				if r.Method != "GET" || r.URL.Path != "/api/v1/namespaces/"+ns {
					t.Error("resource worker changed namespace state")
					w.WriteHeader(500)
					return
				}
				if phase == "denied" {
					w.WriteHeader(403)
					return
				}
				if phase == "absent" {
					w.WriteHeader(404)
					return
				}
				labels := labels(id)
				labels[allocation.MarkerLabel] = allocator.Marker()
				labels[allocation.ProfileLabel] = "database"
				if phase == "foreign" {
					labels[allocation.MarkerLabel] = "other"
				}
				if phase == "wrong-profile" {
					labels[allocation.ProfileLabel] = "gateway"
				}
				if phase == "wrong-owner" {
					labels[ownerLabel] = "foreign"
				}
				meta := object{"uid": "namespace-uid", "resourceVersion": "1", "labels": labels}
				if phase == "deleting" {
					meta["deletionTimestamp"] = "2026-09-12T00:00:00Z"
				}
				_ = json.NewEncoder(w).Encode(object{"metadata": meta})
			})
			var err error
			allocator, err = allocation.New(k.client, "control")
			if err != nil {
				t.Fatal(err)
			}
			c := &CNPG{client: k.client, allocation: allocator, cluster: testClusterID}
			err = c.Delete(context.Background(), db)
			switch phase {
			case "absent":
				if err != nil {
					t.Fatal(err)
				}
			case "foreign", "wrong-profile", "wrong-owner", "denied":
				if err == nil || errors.Is(err, ErrPending) {
					t.Fatal("foreign allocation accepted", err)
				}
			default:
				if !errors.Is(err, ErrPending) {
					t.Fatal("cleanup completed before namespace removal", err)
				}
			}
			if phase == "absent" || phase == "deleting" {
				if err := c.Ensure(context.Background(), db); !errors.Is(err, ErrPending) {
					t.Fatal("missing allocation did not stop provisioning", err)
				}
			}
		})
	}
}
