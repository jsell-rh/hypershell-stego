package gatewayworkload

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"google.golang.org/protobuf/proto"
)

func TestSharedRoleUpdateRejectsStaleListsAndPreservesOtherGateways(t *testing.T) {
	current := object{"metadata": object{"name": "openshell-db", "labels": object{managerLabel: "hypershell-database-controller", "hypershell.redhat.io/database-id": "database"}, "uid": "cluster-uid", "resourceVersion": "1"}, "spec": object{"managed": object{"roles": []any{}}}}
	clone := func(value object) object {
		encoded, _ := json.Marshal(value)
		var out object
		_ = json.Unmarshal(encoded, &out)
		return out
	}
	current = clone(current)
	writes := 0
	k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "PATCH" {
			t.Error("role update performed another read", r.Method)
			w.WriteHeader(500)
			return
		}
		var patch object
		if json.NewDecoder(r.Body).Decode(&patch) != nil {
			t.Error("invalid patch")
			w.WriteHeader(400)
			return
		}
		writes++
		if kube.String(patch, "metadata", "uid") != "cluster-uid" || kube.String(patch, "metadata", "resourceVersion") != kube.String(current, "metadata", "resourceVersion") {
			w.WriteHeader(409)
			return
		}
		current["spec"] = patch["spec"]
		current["metadata"].(map[string]any)["resourceVersion"] = string(rune('1' + writes))
		_ = json.NewEncoder(w).Encode(current)
	})
	original := clone(current)
	first := desiredRole("gw_first", "first-secret", "first", false)
	second := desiredRole("gw_second", "second-secret", "second", false)
	if _, err := k.setSharedRole(context.Background(), "database", "database", original, first); err != nil {
		t.Fatal(err)
	}
	if _, err := k.setSharedRole(context.Background(), "database", "database", original, second); err == nil {
		t.Fatal("stale role list replaced another Gateway")
	}
	if _, err := k.setSharedRole(context.Background(), "database", "database", clone(current), second); err != nil {
		t.Fatal(err)
	}
	roles := kube.Nested(current, "spec", "managed", "roles").([]any)
	if len(roles) != 2 || kube.String(roles[0].(map[string]any), "name") != "gw_first" || kube.String(roles[1].(map[string]any), "name") != "gw_second" {
		t.Fatal("lost another Gateway role")
	}
	before := writes
	if _, err := k.setSharedRole(context.Background(), "database", "database", clone(current), second); err != nil || writes != before {
		t.Fatal("stable role update wrote the Cluster", err, writes)
	}
	if _, err := k.setSharedRole(context.Background(), "database", "database", clone(current), desiredRole("gw_first", "", "first", true)); err != nil {
		t.Fatal(err)
	}
	roles = kube.Nested(current, "spec", "managed", "roles").([]any)
	if !reflect.DeepEqual(roles[1], clone(object{"role": second})["role"]) {
		t.Fatal("role deletion changed another Gateway")
	}
	if _, err := k.setSharedRole(context.Background(), "database", "database", clone(current), desiredRole("gw_second", "other", "foreign", false)); err == nil {
		t.Fatal("adopted a foreign role")
	}
}

func TestAllocatedGatewayCleanupCannotSkipItsSQLDatabase(t *testing.T) {
	for _, label := range []string{"cnpg", "deployment", "unknown", "", "foreign-allocation", "foreign-cluster", "removed-record"} {
		t.Run(label, func(t *testing.T) {
			gw, db, _ := localRecords(t)
			var k *Kubernetes
			writes, requests := 0, 0
			name, _ := sharedResourceName(gw.Metadata.Id)
			k = fixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				switch r.URL.Path {
				case "/api/v1/namespaces/" + gw.Namespace:
					if r.Method != "GET" {
						t.Error("worker wrote Gateway namespace")
						w.WriteHeader(500)
						return
					}
					w.WriteHeader(404)
				case "/api/v1/namespaces/" + db.Namespace:
					if r.Method != "GET" {
						t.Error("worker wrote database namespace")
						w.WriteHeader(500)
						return
					}
					labels := object{managerLabel: "hypershell-database-controller", "hypershell.redhat.io/database-id": db.Metadata.Id, "hypershell.redhat.io/database-provider": label, allocation.MarkerLabel: k.allocation.Marker(), allocation.ProfileLabel: "database"}
					if label == "foreign-allocation" {
						labels[allocation.MarkerLabel] = "foreign"
					}
					_ = json.NewEncoder(w).Encode(object{"metadata": object{"name": db.Namespace, "uid": "ns-uid", "resourceVersion": "1", "labels": labels}})
				case sharedAPI + db.Namespace + "/clusters/openshell-db":
					if r.Method != "GET" {
						t.Error("changed shared server before database removal")
						w.WriteHeader(500)
						return
					}
					_ = json.NewEncoder(w).Encode(object{"metadata": object{"name": "openshell-db", "uid": "cluster-uid", "resourceVersion": "1", "labels": object{managerLabel: "hypershell-database-controller", "hypershell.redhat.io/database-id": db.Metadata.Id}}})
				case sharedAPI + db.Namespace + "/databases/" + name:
					switch r.Method {
					case "GET":
						resource := sharedDefinition("Database", name, gw.Metadata.Id, db.Metadata.Id)
						resource["metadata"].(object)["uid"] = "database-uid"
						resource["metadata"].(object)["resourceVersion"] = "7"
						_ = json.NewEncoder(w).Encode(resource)
					case "DELETE":
						writes++
						var options object
						if json.NewDecoder(r.Body).Decode(&options) != nil || kube.String(options, "preconditions", "uid") != "database-uid" || kube.String(options, "preconditions", "resourceVersion") != "7" {
							t.Error("SQL resource deletion lost identity checks")
						}
						w.WriteHeader(202)
						_ = json.NewEncoder(w).Encode(object{"kind": "Status"})
					default:
						t.Error("unexpected SQL resource operation")
						w.WriteHeader(500)
					}
				default:
					t.Error("unexpected cleanup request", r.URL.Path)
					w.WriteHeader(500)
				}
			})
			a, err := allocation.New(k.client, "control")
			if err != nil {
				t.Fatal(err)
			}
			k.allocation = a
			if label == "foreign-cluster" {
				db.ClusterId = proto.String("000000000000000000000000002")
			}
			if label == "removed-record" {
				db.Provider = "deployment"
			}
			err = k.Delete(context.Background(), gw, db)
			if label == "foreign-allocation" || label == "foreign-cluster" || label == "removed-record" {
				if err == nil || errors.Is(err, ErrPending) || writes != 0 {
					t.Fatal("invalid placement caused SQL cleanup", err, writes)
				}
				if label != "foreign-allocation" && requests != 0 {
					t.Fatal("invalid record reached Kubernetes")
				}
			} else if !errors.Is(err, ErrPending) || writes != 1 {
				t.Fatal("allocated namespace skipped SQL cleanup", err, writes)
			}
		})
	}
}
