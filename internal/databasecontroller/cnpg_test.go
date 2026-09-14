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
	return &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, Provider: "cnpg", ClusterId: proto.String(testClusterID)}
}
func testCNPG(t *testing.T, k *Kubernetes) *CNPG {
	t.Helper()
	a, err := allocation.New(k.client, "control")
	if err != nil {
		t.Fatal(err)
	}
	return &CNPG{client: k.client, allocation: a, cluster: testClusterID}
}
func TestCNPGRequiresExplicitAllocationAndCluster(t *testing.T) {
	for _, o := range []KubernetesOptions{{}, {ClusterID: testClusterID}, {ControlNamespace: "control"}, {ClusterID: "bad", ControlNamespace: "control"}} {
		if c, err := NewCNPG(o); err == nil {
			c.Close()
			t.Fatal("incomplete placement accepted")
		}
	}
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
			c := testCNPG(t, k)
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
	c := testCNPG(t, k)
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
func TestCNPGControllerSelectsProviderAndDoesNotInventCredentials(t *testing.T) {
	db := cnpgRow()
	api := &stateAPI{db: db}
	provider := &recordingProvider{}
	controller, err := NewForProvider(api, api, "cnpg", testClusterID, provider)
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

func TestCNPGWritesOnlyInsideVerifiedAllocation(t *testing.T) {
	for _, phase := range []string{"active", "absent", "deleting", "foreign", "wrong-cluster"} {
		t.Run(phase, func(t *testing.T) {
			db := cnpgRow()
			var c *CNPG
			reads, writes := 0, 0
			k, _ := testKubernetes(t, func(w http.ResponseWriter, r *http.Request) {
				reads++
				switch r.URL.Path {
				case cnpgAPI:
					if r.Method != "GET" {
						t.Error("changed API discovery")
						w.WriteHeader(500)
						return
					}
					_ = json.NewEncoder(w).Encode(cnpgDiscovery())
				case "/api/v1/namespaces/" + db.Namespace:
					if r.Method != "GET" {
						t.Error("worker wrote a namespace")
						w.WriteHeader(500)
						return
					}
					if phase == "absent" {
						w.WriteHeader(404)
						return
					}
					label := labels(db.Metadata.Id)
					label[allocation.MarkerLabel], label[allocation.ProfileLabel] = c.allocation.Marker(), "database"
					if phase == "foreign" {
						label[allocation.MarkerLabel] = "foreign"
					}
					meta := object{"uid": "ns-uid", "resourceVersion": "1", "labels": label}
					if phase == "deleting" {
						meta["deletionTimestamp"] = "2026-09-14T00:00:00Z"
					}
					_ = json.NewEncoder(w).Encode(object{"metadata": meta})
				case cnpgAPI + "/namespaces/" + db.Namespace + "/clusters/" + CNPGClusterName:
					if r.Method != "GET" {
						t.Error("unexpected resource update")
						w.WriteHeader(500)
						return
					}
					w.WriteHeader(404)
				case cnpgAPI + "/namespaces/" + db.Namespace + "/clusters":
					if r.Method != "POST" || phase != "active" {
						t.Error("resource write without allocation")
						w.WriteHeader(500)
						return
					}
					writes++
					var cluster object
					if err := json.NewDecoder(r.Body).Decode(&cluster); err != nil {
						t.Error(err)
						w.WriteHeader(500)
						return
					}
					if !owner(db.Metadata.Id).Matches(cluster) || !contains(cluster, cnpgDefinition(db.Metadata.Id)) {
						t.Error("incorrect CNPG intent")
					}
					cluster["metadata"].(map[string]any)["uid"] = "cluster-uid"
					cluster["metadata"].(map[string]any)["resourceVersion"] = "1"
					w.WriteHeader(201)
					_ = json.NewEncoder(w).Encode(cluster)
				default:
					t.Error("unexpected Kubernetes request", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			})
			c = testCNPG(t, k)
			if phase == "wrong-cluster" {
				db.ClusterId = proto.String("000000000000000000000000002")
			}
			err := c.Ensure(context.Background(), db)
			if phase == "active" || phase == "absent" || phase == "deleting" {
				if !errors.Is(err, ErrPending) {
					t.Fatal("readiness is not pending", err)
				}
			} else if err == nil || errors.Is(err, ErrPending) {
				t.Fatal("invalid placement accepted", err)
			}
			expected := 0
			if phase == "active" {
				expected = 1
			}
			if writes != expected {
				t.Fatal("wrong resource write count", writes)
			}
			if phase == "wrong-cluster" && reads != 0 {
				t.Fatal("foreign placement reached Kubernetes")
			}
		})
	}
}
