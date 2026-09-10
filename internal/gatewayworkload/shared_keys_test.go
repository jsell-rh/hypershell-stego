package gatewayworkload

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"github.com/segmentio/ksuid"
)

type keyAPI struct {
	mu         sync.Mutex
	objects    map[string]object
	writes     int
	denyMarker bool
}

func (a *keyAPI) serve(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		if r.URL.Path == "/apis/postgresql.cnpg.io/v1" {
			_ = json.NewEncoder(w).Encode(object{"kind": "APIResourceList", "groupVersion": "postgresql.cnpg.io/v1", "resources": []object{{"name": "databases", "kind": "Database", "namespaced": true, "verbs": []string{"get"}}}})
			return
		}
		if r.Method == "GET" {
			value, found := a.objects[r.URL.Path]
			if !found {
				w.WriteHeader(404)
				return
			}
			_ = json.NewEncoder(w).Encode(value)
			return
		}
		if r.Method != "POST" {
			t.Error("key initialization changed an existing resource")
			w.WriteHeader(500)
			return
		}
		a.writes++
		if a.denyMarker && strings.HasSuffix(r.URL.Path, "/configmaps") {
			w.WriteHeader(403)
			return
		}
		var value object
		if json.NewDecoder(r.Body).Decode(&value) != nil {
			t.Error("invalid key write")
			w.WriteHeader(400)
			return
		}
		name := kube.String(value, "metadata", "name")
		path := r.URL.Path + "/" + name
		if _, found := a.objects[path]; found {
			w.WriteHeader(409)
			return
		}
		meta := value["metadata"].(map[string]any)
		meta["uid"] = name
		meta["resourceVersion"] = "1"
		a.objects[path] = value
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(value)
	}
}
func sharedKeyFixture(t *testing.T) (*Kubernetes, *keyAPI, *pb.Gateway, *pb.ManagedDatabase) {
	t.Helper()
	gw, db, _ := records(t)
	db.Provider = gateways.ProviderCNPG
	ns := object{"metadata": object{"uid": "database-ns", "resourceVersion": "1", "name": db.Namespace, "labels": object{managerLabel: "hypershell-database-controller", "hypershell.redhat.io/database-id": db.Metadata.Id}}}
	api := &keyAPI{objects: map[string]object{"/api/v1/namespaces/" + db.Namespace: ns}}
	return fixture(t, api.serve(t)), api, gw, db
}
func TestSharedDatabaseKeysRemainSeparateAcrossGatewaysAndRestart(t *testing.T) {
	k, api, first, db := sharedKeyFixture(t)
	second, _, _ := records(t)
	second.DatabaseId = db.Metadata.Id
	var group sync.WaitGroup
	results := make([]object, 2)
	for i, gw := range []*pb.Gateway{first, second} {
		group.Add(1)
		go func() {
			defer group.Done()
			value, err := checkedFixtureKeys(k, context.Background(), gw, db)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = value
		}()
	}
	group.Wait()
	if results[0] == nil || results[1] == nil || reflect.DeepEqual(results[0], results[1]) {
		t.Fatal("shared database did not isolate Gateway keys")
	}
	restarted := fixture(t, api.serve(t))
	for i, gw := range []*pb.Gateway{first, second} {
		value, err := checkedFixtureKeys(restarted, context.Background(), gw, db)
		if err != nil || !reflect.DeepEqual(value, results[i]) {
			t.Fatal("restart changed shared Gateway keys", err)
		}
	}
	if api.writes != 4 || len(api.objects) != 5 {
		t.Fatal("stable reads wrote key resources", api.writes)
	}
	ns := api.objects["/api/v1/namespaces/"+db.Namespace]
	if kube.String(ns, "metadata", "labels", ownerLabel) != "" || kube.String(ns, "metadata", "annotations", keysMarker) != "" {
		t.Fatal("shared namespace was bound to one Gateway")
	}
}
func TestSharedDatabaseKeysRecoverIncompletePinWithoutRekeying(t *testing.T) {
	k, api, gw, db := sharedKeyFixture(t)
	api.denyMarker = true
	if _, err := checkedFixtureKeys(k, context.Background(), gw, db); err == nil {
		t.Fatal("uncommitted key identity was returned")
	}
	name, _ := sharedResourceName(gw.Metadata.Id)
	path := "/api/v1/namespaces/" + db.Namespace + "/secrets/" + name + "-keys"
	original := api.objects[path]
	if original == nil {
		t.Fatal("source Secret was not created")
	}
	api.denyMarker = false
	value, err := checkedFixtureKeys(k, context.Background(), gw, db)
	if err != nil {
		t.Fatal(err)
	}
	if value["kid"] != kube.String(original, "data", "kid") || value["key-encryption-key"] != kube.String(original, "data", "key-encryption-key") {
		t.Fatal("pin retry generated different material")
	}
}
func TestSharedDatabaseKeysRejectLossReplacementAndForeignOwners(t *testing.T) {
	for _, mode := range []string{"lost-secret", "replacement", "foreign-secret", "foreign-marker", "foreign-namespace", "missing-identity", "lost-both-with-database"} {
		t.Run(mode, func(t *testing.T) {
			k, api, gw, db := sharedKeyFixture(t)
			if _, err := checkedFixtureKeys(k, context.Background(), gw, db); err != nil {
				t.Fatal(err)
			}
			name, _ := sharedResourceName(gw.Metadata.Id)
			core := "/api/v1/namespaces/" + db.Namespace
			secretPath, markerPath := core+"/secrets/"+name+"-keys", core+"/configmaps/"+name+"-key-identity"
			switch mode {
			case "lost-secret":
				delete(api.objects, secretPath)
			case "replacement":
				value, err := newKeys()
				if err != nil {
					t.Fatal(err)
				}
				api.objects[secretPath]["data"] = value
			case "foreign-secret":
				api.objects[secretPath]["metadata"].(map[string]any)["labels"].(map[string]any)[ownerLabel] = "foreign"
			case "foreign-marker":
				api.objects[markerPath]["metadata"].(map[string]any)["labels"].(map[string]any)[ownerLabel] = "foreign"
			case "foreign-namespace":
				api.objects[core]["metadata"].(object)["labels"].(object)["hypershell.redhat.io/database-id"] = "foreign"
			case "missing-identity":
				delete(api.objects[markerPath]["metadata"].(map[string]any), "uid")
			case "lost-both-with-database":
				delete(api.objects, secretPath)
				delete(api.objects, markerPath)
				api.objects["/apis/postgresql.cnpg.io/v1/namespaces/"+db.Namespace+"/databases/"+name] = object{"kind": "Database"}
			}
			before := api.writes
			if _, err := checkedFixtureKeys(k, context.Background(), gw, db); err == nil {
				t.Fatal("unsafe shared key material was accepted")
			}
			if api.writes != before {
				t.Fatal("unsafe key state caused a write")
			}
		})
	}
}
func TestSharedNamesPreserveEveryIDByte(t *testing.T) {
	// The two IDs differ only by letter case but are both canonical KSUIDs.
	ids := []string{"0ujtsYcgvSTl8PAuAdqWYSMnLOv", "0ujtsycgvSTl8PAuAdqWYSMnLOv"}
	names := map[string]bool{}
	for _, id := range ids {
		parsed, err := ksuid.Parse(id)
		if err != nil || parsed.String() != id {
			t.Fatal("invalid fixture", err)
		}
		name, err := sharedResourceName(id)
		if err != nil || names[name] || !dnsLabel.MatchString(name) || len(name+"-key-identity") > 63 {
			t.Fatal("shared name lost ID identity", err)
		}
		names[name] = true
	}
}

func TestSharedKeyInitializationConvergesAcrossConcurrentWriters(t *testing.T) {
	k, api, gw, db := sharedKeyFixture(t)
	var group sync.WaitGroup
	results := make([]object, 4)
	for i := range results {
		group.Add(1)
		go func() {
			defer group.Done()
			for attempt := 0; attempt < 5; attempt++ {
				value, err := checkedFixtureKeys(k, context.Background(), gw, db)
				if err == nil {
					results[i] = value
					return
				}
				var failure *kube.APIError
				if !errors.As(err, &failure) || failure.StatusCode != http.StatusConflict {
					t.Error(err)
					return
				}
			}
			t.Error("key initialization did not converge")
		}()
	}
	group.Wait()
	for _, result := range results {
		if result == nil || !reflect.DeepEqual(result, results[0]) {
			t.Fatal("writers returned different keys")
		}
	}
	if len(api.objects) != 3 {
		t.Fatal("writers created more than one source and identity")
	}
}

// These tests isolate the key protocol. The live CNPG workflow supplies the
// actual SQL evidence through sharedKeys.
func checkedFixtureKeys(k *Kubernetes, ctx context.Context, gw *pb.Gateway, db *pb.ManagedDatabase) (object, error) {
	return k.sharedKeysChecked(ctx, gw, db, func() error { return nil })
}
func TestSharedKeysRequireAbsenceEvidenceBeforeTheFirstWrite(t *testing.T) {
	k, api, gw, db := sharedKeyFixture(t)
	for _, check := range []func() error{nil, func() error { return errors.New("SQL state is not absent") }} {
		if _, err := k.sharedKeysChecked(context.Background(), gw, db, check); err == nil {
			t.Fatal("key creation had no SQL absence evidence")
		}
		if api.writes != 0 {
			t.Fatal("failed SQL check wrote keys")
		}
	}
}
