package databasecontroller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
)

func TestAllocatedDatabaseOnlyObservesNamespaceCleanup(t *testing.T) {
	id := testClusterID
	ns, _ := gateways.DatabaseNamespace(id)
	db := &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: id}, Provider: "deployment", Namespace: ns}
	for _, phase := range []string{"active", "deleting", "absent", "foreign"} {
		t.Run(phase, func(t *testing.T) {
			var allocator *allocation.Allocator
			k, _ := testKubernetes(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v1/namespaces/"+ns {
					t.Error("resource worker changed namespace state")
					w.WriteHeader(500)
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
			k.allocation = allocator
			err = k.Delete(context.Background(), db)
			switch phase {
			case "absent":
				if err != nil {
					t.Fatal(err)
				}
			case "foreign":
				if err == nil || errors.Is(err, ErrPending) {
					t.Fatal("foreign allocation accepted", err)
				}
			default:
				if !errors.Is(err, ErrPending) {
					t.Fatal("cleanup completed before namespace removal", err)
				}
			}
			if phase == "absent" {
				if err := k.Ensure(context.Background(), db); !errors.Is(err, ErrPending) {
					t.Fatal("missing allocation did not stop provisioning", err)
				}
			}
		})
	}
}
