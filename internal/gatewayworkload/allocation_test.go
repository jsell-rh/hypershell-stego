package gatewayworkload

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func TestAllocatedKeysWaitForSealAndRejectKeyLoss(t *testing.T) {
	gw, db, _ := records(t)
	var secret, record object
	sealed := false
	secretWrites, recordWrites := 0, 0
	var allocator *allocation.Allocator
	k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		core := "/api/v1/namespaces/" + db.Namespace
		if r.URL.Path == core {
			if r.Method != "GET" {
				t.Error("resource worker wrote a Namespace")
				w.WriteHeader(500)
				return
			}
			labels := object{"hypershell.redhat.io/database-id": db.Metadata.Id, managerLabel: "hypershell-database-controller", allocation.MarkerLabel: allocator.Marker(), allocation.ProfileLabel: "database"}
			meta := object{"uid": "namespace-uid", "resourceVersion": "4", "labels": labels}
			if sealed {
				labels[ownerLabel] = gw.Metadata.Id
				meta["annotations"] = object{keysMarker: kube.String(record, "data", "fingerprint")}
			}
			_ = json.NewEncoder(w).Encode(object{"metadata": meta})
			return
		}
		var target *object
		switch r.URL.Path {
		case core + "/secrets/" + keysName, core + "/secrets":
			target = &secret
		case core + "/configmaps/openshell-key-identity", core + "/configmaps":
			target = &record
		default:
			t.Error("unexpected key request", r.URL.Path)
			w.WriteHeader(500)
			return
		}
		if r.Method == "POST" {
			if *target != nil {
				t.Error("identity overwrite")
				w.WriteHeader(409)
				return
			}
			if json.NewDecoder(r.Body).Decode(target) != nil {
				t.Fatal("invalid key request")
			}
			if target == &secret {
				secretWrites++
			} else {
				recordWrites++
				fields, ok := (*target)["data"].(map[string]any)
				if !ok || len(fields) != 2 || fields["gateway"] != gw.Metadata.Id || fields["fingerprint"] == "" || (*target)["immutable"] != true {
					t.Error("public identity contains the wrong fields")
				}
			}
			w.WriteHeader(201)
		} else if r.Method != "GET" {
			t.Error("unexpected identity mutation")
			w.WriteHeader(500)
			return
		}
		if *target == nil {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(*target)
	})
	var err error
	allocator, err = allocation.New(k.client, "control")
	if err != nil {
		t.Fatal(err)
	}
	k.allocation = allocator
	for attempt := 0; attempt < 2; attempt++ {
		if _, err := k.keys(context.Background(), gw, db); !errors.Is(err, ErrPending) {
			t.Fatal("unsealed key became usable", err)
		}
	}
	if secretWrites != 1 || recordWrites != 1 {
		t.Fatal("identity writes did not converge")
	}
	sealed = true
	if _, err := k.keys(context.Background(), gw, db); err != nil {
		t.Fatal("sealed key failed", err)
	}
	saved := secret
	secret = nil
	if _, err := k.keys(context.Background(), gw, db); err == nil {
		t.Fatal("lost sealed key was replaced")
	}
	sealed = false
	if _, err := k.keys(context.Background(), gw, db); err == nil {
		t.Fatal("public record did not protect an unsealed lost key")
	}
	if secretWrites != 1 {
		t.Fatal("key loss caused rekeying")
	}
	secret = saved
	record["data"].(map[string]any)["fingerprint"] = "changed"
	if _, err := k.keys(context.Background(), gw, db); err == nil {
		t.Fatal("changed public identity was accepted")
	}
}
