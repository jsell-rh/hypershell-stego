package acceptance

import (
	"context"
	"encoding/json"
	"reflect"
	"regexp"
	"time"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	postgres "github.com/jsell-rh/hypershell-stego/out/postgres"
)

func (w *browserGatewayWorkload) gatewaySQLObjectIDs() map[string][3]string {
	w.t.Helper()
	result := map[string][3]string{}
	for _, id := range w.gatewayIDs {
		options, _ := w.sqlOptions(id)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		var objects [3]string
		err := postgres.ReadRow(ctx, options, `SELECT d.oid::text,r.oid::text,d.datdba::text FROM pg_catalog.pg_database d JOIN pg_catalog.pg_roles r ON r.rolname=current_user WHERE d.datname=current_database()`, nil, &objects[0], &objects[1], &objects[2])
		cancel()
		if err != nil || objects[0] == "" || objects[1] == "" || objects[2] == "" {
			w.t.Fatal("database restart SQL identity read failed")
		}
		result[id] = objects
	}
	return result
}

// Delete exactly one observed primary Pod. Keep the installation Cluster and
// storage. A new Pod UID and ready SQL clients must prove recovery.
func (w *browserGatewayWorkload) restartCNPGDatabase() map[string]any {
	w.t.Helper()
	fixture := w.cnpgFixture
	cluster := w.requireCNPGInstallation()
	originalSpec := kube.Nested(cluster, "spec")
	primary := func(cluster kube.Object) (string, bool) {
		w.t.Helper()
		var state struct {
			Spec   struct{ Instances int }
			Status struct {
				CurrentPrimary string
				ReadyInstances int
			}
		}
		data, err := json.Marshal(cluster)
		if err != nil || json.Unmarshal(data, &state) != nil || state.Spec.Instances != 2 ||
			!regexp.MustCompile(`^gateway-database-[1-9][0-9]*$`).MatchString(state.Status.CurrentPrimary) {
			w.t.Fatal("CNPG restart requires the two-instance installation fixture")
		}
		return state.Status.CurrentPrimary, state.Status.ReadyInstances == 2
	}
	readPod := func(name string) (kube.Object, bool) {
		w.t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		pod, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+fixture.Namespace+"/pods/"+name, nil)
		if err == nil && code == 404 {
			return nil, false
		}
		if err != nil || code != 200 {
			w.t.Fatal("CNPG restart Pod read failed", code)
		}
		var state struct {
			Metadata struct {
				UID             string
				OwnerReferences []struct {
					APIVersion, Kind, Name, UID string
					Controller                  bool
				}
			}
			Status struct {
				Conditions []struct{ Type, Status string }
			}
		}
		data, err := json.Marshal(pod)
		if err != nil || json.Unmarshal(data, &state) != nil || state.Metadata.UID == "" || kube.String(pod, "metadata", "labels", "cnpg.io/cluster") != fixture.Cluster {
			w.t.Fatal("CNPG restart Pod identity is invalid")
		}
		owners := 0
		for _, ref := range state.Metadata.OwnerReferences {
			if ref.Controller {
				if ref.APIVersion != "postgresql.cnpg.io/v1" || ref.Kind != "Cluster" || ref.Name != fixture.Cluster || ref.UID != fixture.ClusterUID {
					w.t.Fatal("CNPG restart Pod has a different controller")
				}
				owners++
			}
		}
		if owners != 1 {
			w.t.Fatal("CNPG restart Pod has no single installation owner")
		}
		for _, condition := range state.Status.Conditions {
			if condition.Type == "Ready" && condition.Status == "True" && kube.String(pod, "metadata", "deletionTimestamp") == "" {
				return pod, true
			}
		}
		return pod, false
	}
	oldPrimary, ready := primary(cluster)
	oldPod, podReady := readPod(oldPrimary)
	if !ready || !podReady {
		w.t.Fatal("CNPG installation was not ready before restart")
	}
	oldUID := kube.String(oldPod, "metadata", "uid")
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, code, err := w.kubernetes.Request(ctx, "DELETE", "/api/v1/namespaces/"+fixture.Namespace+"/pods/"+oldPrimary,
		kube.Object{"apiVersion": "v1", "kind": "DeleteOptions", "gracePeriodSeconds": 30, "preconditions": kube.Object{"uid": oldUID}, "propagationPolicy": "Background"})
	cancel()
	if err != nil || code != 200 && code != 202 {
		w.t.Fatal("CNPG restart Pod deletion was not accepted", code)
	}
	deadline := time.Now().Add(240 * time.Second)
	for {
		cluster = w.requireCNPGInstallation()
		if !reflect.DeepEqual(originalSpec, kube.Nested(cluster, "spec")) {
			w.t.Fatal("CNPG restart changed the installation specification")
		}
		currentPrimary, ready := primary(cluster)
		currentPod, podReady := readPod(currentPrimary)
		old, _ := readPod(oldPrimary)
		currentUID := kube.String(currentPod, "metadata", "uid")
		if ready && podReady && currentUID != oldUID && kube.String(old, "metadata", "uid") != oldUID {
			return map[string]any{"namespace": fixture.Namespace, "namespace_uid": fixture.NamespaceUID, "cluster_uid": fixture.ClusterUID,
				"old_primary": oldPrimary, "new_primary": currentPrimary, "primary_changed": oldPrimary != currentPrimary,
				"old_pod_uid": oldUID, "new_primary_pod_uid": currentUID, "ready_instances": 2, "cluster_spec_unchanged": true, "seconds": time.Since(started).Seconds()}
		}
		if time.Now().After(deadline) {
			w.t.Fatal("CNPG Pod replacement and readiness were not confirmed")
		}
		time.Sleep(2 * time.Second)
	}
}
