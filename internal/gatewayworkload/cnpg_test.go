package gatewayworkload

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"testing"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
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

func TestSharedDatabaseCleanupRejectsAChangedProviderLabel(t *testing.T) {
	gw, db, _ := records(t)
	requests := 0
	k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method != "GET" {
			t.Error("provider conflict caused an effect")
			w.WriteHeader(500)
			return
		}
		if r.URL.Path == "/api/v1/namespaces/"+db.Namespace {
			_ = json.NewEncoder(w).Encode(object{"metadata": object{"name": db.Namespace, "uid": "namespace-uid", "resourceVersion": "1", "labels": object{managerLabel: "hypershell-database-controller", "hypershell.redhat.io/database-id": db.Metadata.Id, providerLabel: "deployment"}}})
			return
		}
		if r.URL.Path == sharedAPI+db.Namespace+"/clusters/openshell-db" {
			_ = json.NewEncoder(w).Encode(object{"metadata": object{"name": "openshell-db"}})
			return
		}
		t.Error("unexpected cleanup request", r.URL.Path)
		w.WriteHeader(500)
	})
	if err := k.deleteSharedDatabase(context.Background(), gw); err == nil {
		t.Fatal("provider label hid a shared Cluster")
	}
	if requests != 2 {
		t.Fatal("provider conflict did not stop cleanup", requests)
	}
}
