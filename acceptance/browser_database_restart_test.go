package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"reflect"
	"time"

	"google.golang.org/protobuf/proto"
)

// Restart only the PostgreSQL sidecar in this Job. Its data volume must survive.
// This proves reconnect behavior, not an installation CNPG or RDS failover.
func (w *browserGatewayWorkload) checkDatabaseRestart(id string) {
	w.t.Helper()
	before := w.checkSQLIsolation()
	owner := w.identity.browserLogin(w.t, w.audience(id), "console-alice")
	provider, err := w.call("GetProvider", owner, `{"name":"browser-provider"}`)
	if err != nil {
		w.t.Fatal("database restart setup could not read provider")
	}
	pod := os.Getenv("HOSTNAME")
	type podState struct {
		Metadata struct {
			UID    string
			Labels map[string]string
		}
		Status struct {
			InitContainerStatuses []struct {
				Name         string
				RestartCount int
				Ready        bool
			}
		}
	}
	read := func() (string, int, bool) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		object, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+w.p.namespace+"/pods/"+pod, nil)
		data, _ := json.Marshal(object)
		var state podState
		if err != nil || code != 200 || json.Unmarshal(data, &state) != nil || state.Metadata.UID == "" || state.Metadata.Labels["app"] != "stego-fixture" || state.Metadata.Labels["job-name"] != "service-check" {
			w.t.Fatal("database restart requires this bounded fixture Pod")
		}
		for _, container := range state.Status.InitContainerStatuses {
			if container.Name == "postgres" {
				return state.Metadata.UID, container.RestartCount, container.Ready
			}
		}
		w.t.Fatal("PostgreSQL sidecar status is absent")
		return "", 0, false
	}
	uid, restarts, ready := read()
	if !ready {
		w.t.Fatal("PostgreSQL sidecar was not ready before restart")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	// The exec response can be lost when the container stops. The Pod status
	// below must prove restart; a missing response is never a pass.
	command := exec.CommandContext(ctx, w.p.oc, "--namespace="+w.p.namespace, "--request-timeout=20s", "exec", pod, "-c", "postgres", "--", "sh", "-c", "kill -INT 1")
	output, signalError := command.CombinedOutput()
	cancel()
	if signalError != nil {
		category := kubernetesWriteFailureCategory(output)
		if category == "Forbidden" || category == "Unauthorized" {
			w.t.Fatal("database restart signal was denied", category)
		}
		w.t.Log("Database restart exec response was not confirmed", category)
	}
	deadline := time.Now().Add(90 * time.Second)
	for {
		current, count, ready := read()
		if current != uid {
			w.t.Fatal("database restart replaced the entire fixture Pod")
		}
		if count > restarts && ready {
			break
		}
		if time.Now().After(deadline) {
			w.t.Fatal("PostgreSQL sidecar restart was not confirmed")
		}
		time.Sleep(time.Second)
	}
	for _, gateway := range w.gatewayIDs {
		w.check(gateway)
	}
	if after := w.checkSQLIsolation(); !reflect.DeepEqual(before, after) {
		w.t.Fatal("database restart changed Gateway SQL identities")
	}
	after, err := w.call("GetProvider", owner, `{"name":"browser-provider"}`)
	if err != nil || !proto.Equal(provider, after) {
		w.t.Fatal("database restart lost Gateway provider data")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	w.requireInstallationData(ctx)
	w.t.Log("PostgreSQL sidecar restart preserved Gateway keys, credentials, provider data, and installation data")
}
