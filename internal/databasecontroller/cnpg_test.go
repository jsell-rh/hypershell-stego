package databasecontroller

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/protobuf/proto"
)

func cnpgDiscovery() object {
	resources := []object{}
	for name, kind := range map[string]string{"clusters": "Cluster", "databases": "Database", "databaseroles": "DatabaseRole"} {
		resources = append(resources, object{"name": name, "kind": kind, "namespaced": true, "verbs": []string{"get", "create", "patch", "delete"}})
	}
	return object{"kind": "APIResourceList", "groupVersion": "postgresql.cnpg.io/v1", "resources": resources}
}
func cnpgRow() *pb.ManagedDatabase {
	id := ksuid.New().String()
	ns, _ := gateways.DatabaseNamespace(id)
	return &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, Provider: "cnpg"}
}
func TestCNPGMissingAPIsPreventEffects(t *testing.T) {
	for _, mode := range []string{"absent", "partial", "version", "kind", "namespace", "verbs", "denied"} {
		t.Run(mode, func(t *testing.T) {
			k, _ := testKubernetes(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != cnpgAPI {
					t.Error("invalid API contract caused an effect")
					w.WriteHeader(500)
					return
				}
				if mode == "absent" {
					w.WriteHeader(404)
					return
				}
				if mode == "denied" {
					w.WriteHeader(403)
					return
				}
				api := cnpgDiscovery()
				resources := api["resources"].([]object)
				switch mode {
				case "partial":
					api["resources"] = resources[:2]
				case "version":
					api["groupVersion"] = "postgresql.cnpg.io/v2"
				case "kind":
					resources[0]["kind"] = "Wrong"
				case "namespace":
					resources[0]["namespaced"] = false
				case "verbs":
					resources[0]["verbs"] = []string{"get"}
				}
				_ = json.NewEncoder(w).Encode(api)
			})
			c := &CNPG{client: k.client}
			if err := c.Ensure(context.Background(), cnpgRow()); err == nil {
				t.Fatal("missing API accepted")
			}
		})
	}
}
func TestCNPGOptionsFailBeforeEffects(t *testing.T) {
	k, _ := testKubernetes(t, func(w http.ResponseWriter, r *http.Request) {
		t.Error("invalid options reached Kubernetes")
		w.WriteHeader(500)
	})
	c := &CNPG{client: k.client}
	db := cnpgRow()
	for _, field := range []**string{&db.Engine, &db.EngineVersion, &db.Region, &db.InstanceClass, &db.ConnectionSecret} {
		*field = proto.String("unsupported")
		if err := c.Ensure(context.Background(), db); err == nil {
			t.Fatal("unsupported option accepted")
		}
		if err := validateCNPGPlacement(db); err != nil {
			t.Fatal("mutable option prevented cleanup", err)
		}
		*field = nil
	}
	db.Namespace = "kube-system"
	if err := c.Delete(context.Background(), db); err == nil {
		t.Fatal("foreign placement accepted")
	}
}
func TestCNPGCleanupChecksClusterBeforeNamespace(t *testing.T) {
	for _, mode := range []string{"foreign", "pending", "denied", "gone"} {
		t.Run(mode, func(t *testing.T) {
			db := cnpgRow()
			namespaceDeletes, clusterDeletes := 0, 0
			k, _ := testKubernetes(t, func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == cnpgAPI {
					_ = json.NewEncoder(w).Encode(cnpgDiscovery())
					return
				}
				ns := r.URL.Path == "/api/v1/namespaces/"+db.Namespace
				if r.Method == "DELETE" {
					if ns {
						namespaceDeletes++
					} else {
						clusterDeletes++
					}
					var options object
					_ = json.NewDecoder(r.Body).Decode(&options)
					if str(options, "preconditions", "uid") != "uid" || str(options, "preconditions", "resourceVersion") != "7" {
						t.Error("missing deletion preconditions")
					}
					if mode == "denied" {
						w.WriteHeader(403)
						return
					}
					_ = json.NewEncoder(w).Encode(object{"kind": "Status"})
					return
				}
				if !ns && mode == "gone" {
					w.WriteHeader(404)
					return
				}
				meta := object{"uid": "uid", "resourceVersion": "7", "labels": labels(db.Metadata.Id)}
				if !ns && mode == "foreign" {
					meta["labels"] = labels("other")
				}
				if !ns && mode == "pending" {
					meta["deletionTimestamp"] = "2026-09-10T00:00:00Z"
				}
				_ = json.NewEncoder(w).Encode(object{"metadata": meta})
			})
			err := (&CNPG{client: k.client}).Delete(context.Background(), db)
			if err == nil {
				t.Fatal("unconfirmed deletion completed")
			}
			if mode == "gone" {
				if namespaceDeletes != 1 || !errors.Is(err, ErrPending) {
					t.Fatal("namespace was not submitted after Cluster absence", err)
				}
			} else if namespaceDeletes != 0 {
				t.Fatal("namespace deleted before Cluster cleanup")
			}
			if mode == "denied" && (clusterDeletes != 1 || errors.Is(err, ErrPending)) {
				t.Fatal("denied Cluster delete was lost", err)
			}
		})
	}
}
func TestCNPGControllerSelectsProviderAndDoesNotInventCredentials(t *testing.T) {
	db := cnpgRow()
	api := &stateAPI{db: db}
	provider := &recordingProvider{}
	controller, err := NewForProvider(api, api, "cnpg", "", provider)
	if err != nil {
		t.Fatal(err)
	}
	if err = controller.reconcile(context.Background(), db.Metadata.Id); err != nil {
		t.Fatal(err)
	}
	if db.GetStatus() != "ready" || db.ConnectionSecret != nil || len(api.updates) != 1 {
		t.Fatal("incorrect CNPG observation")
	}
	if err = controller.reconcile(context.Background(), db.Metadata.Id); err != nil || len(api.updates) != 1 {
		t.Fatal("stable observation wrote again", err)
	}
	db.Provider = "deployment"
	if err = controller.reconcile(context.Background(), db.Metadata.Id); err != nil || len(api.updates) != 1 || len(provider.ensured) != 2 || len(provider.deleted) != 0 {
		t.Fatal("other provider was changed", err)
	}
	if _, err = NewForProvider(api, api, "unknown", "", provider); err == nil {
		t.Fatal("unknown provider accepted")
	}
}
func TestCNPGPodReadinessRequiresCurrentOwnerAndRunningImage(t *testing.T) {
	original := object{"metadata": object{"ownerReferences": []object{{"apiVersion": "postgresql.cnpg.io/v1", "kind": "Cluster", "name": CNPGClusterName, "uid": "current", "controller": true}}}, "spec": object{"containers": []object{{"name": "postgres", "image": CNPGPostgresImage, "resources": nested(cnpgDefinition(""), "spec", "resources")}}}, "status": object{"phase": "Running", "conditions": []object{{"type": "Ready", "status": "True"}}, "containerStatuses": []object{{"name": "postgres", "ready": true, "state": object{"running": object{}}}}}}
	body, _ := json.Marshal(original)
	for _, mode := range []string{"ready", "old-owner", "old-image", "old-resources", "not-ready", "terminating", "not-running"} {
		t.Run(mode, func(t *testing.T) {
			var pod object
			_ = json.Unmarshal(body, &pod)
			switch mode {
			case "old-owner":
				nested(pod, "metadata", "ownerReferences").([]any)[0].(map[string]any)["uid"] = "old"
			case "old-image":
				nested(pod, "spec", "containers").([]any)[0].(map[string]any)["image"] = "old"
			case "old-resources":
				nested(pod, "spec", "containers").([]any)[0].(map[string]any)["resources"] = map[string]any{}
			case "not-ready":
				nested(pod, "status", "conditions").([]any)[0].(map[string]any)["status"] = "False"
			case "terminating":
				pod["metadata"].(map[string]any)["deletionTimestamp"] = "2026-09-10T00:00:00Z"
			case "not-running":
				nested(pod, "status", "containerStatuses").([]any)[0].(map[string]any)["state"] = map[string]any{"waiting": map[string]any{}}
			}
			if cnpgPodReady(pod, "current") != (mode == "ready") {
				t.Fatal("wrong readiness result")
			}
		})
	}
}
