package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	postgres "github.com/jsell-rh/hypershell-stego/out/postgres"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Lose only the observed workload namespace. Keep controllers running and keep
// the Gateway, retained source state, and supplied PostgreSQL server in place.
func (w *browserGatewayWorkload) checkNamespaceReplacement(id string, viewer *consoleBrowser) {
	w.t.Helper()
	namespace, err := gatewayworkload.Namespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	state, err := gatewayworkload.StateNamespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	read := func(path string) kube.Object {
		w.t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		object, code, err := w.kubernetes.Request(ctx, http.MethodGet, path, nil)
		if err != nil || code != 200 || kube.String(object, "metadata", "uid") == "" {
			w.t.Fatal("namespace recovery object is unavailable", code)
		}
		return object
	}
	namespacePath := "/api/v1/namespaces/" + namespace
	statePath := "/api/v1/namespaces/" + state
	deploymentPath := "/apis/apps/v1/namespaces/" + namespace + "/deployments/openshell-gateway"
	beforeNamespace := read(namespacePath)
	beforeDeployment := read(deploymentPath)
	beforeState := read(statePath)
	beforeSource := read(statePath + "/secrets/openshell-gateway-state")
	fingerprint := kube.String(beforeState, "metadata", "annotations", "hypershell.redhat.io/state-identity")
	if fingerprint == "" || kube.Nested(beforeSource, "data") == nil {
		w.t.Fatal("namespace recovery requires sealed source state")
	}
	beforeSQL := w.checkSQLIsolation()
	controllers := w.namespaceRecoveryControllers()
	options, _ := w.sqlOptions(id)
	// Object IDs distinguish retained SQL objects from a drop and recreation
	// with the same names. No credential or provider value enters the evidence.
	sqlIdentity := func(options postgres.Options) [3]string {
		w.t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var ids [3]string
		err := postgres.ReadRow(ctx, options, `SELECT d.oid::text,r.oid::text,d.datdba::text FROM pg_catalog.pg_database d JOIN pg_catalog.pg_roles r ON r.rolname=current_user WHERE d.datname=current_database()`, nil, &ids[0], &ids[1], &ids[2])
		if err != nil || ids[0] == "" || ids[1] == "" || ids[2] == "" {
			w.t.Fatal("namespace recovery SQL identity read failed")
		}
		return ids
	}
	beforeSQLObjects := sqlIdentity(options)
	otherOptions := map[string]postgres.Options{}
	otherSQLObjects := map[string][3]string{}
	for _, gatewayID := range w.gatewayIDs {
		if gatewayID != id {
			otherOptions[gatewayID], _ = w.sqlOptions(gatewayID)
			otherSQLObjects[gatewayID] = sqlIdentity(otherOptions[gatewayID])
		}
	}
	owner := w.identity.browserLogin(w.t, w.audience(id), "console-alice")
	provider, err := w.call("GetProvider", owner, `{"name":"browser-provider"}`)
	if err != nil {
		w.t.Fatal("namespace recovery setup could not read provider")
	}
	checkViewer := w.startViewerWorkflow(id, viewer, owner, provider)
	allocator, err := allocation.New(w.kubernetes, w.p.namespace)
	if err != nil {
		w.t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	if err := allocator.RequireNamespace(ctx, "gateway", namespace, id); err != nil {
		cancel()
		w.t.Fatal("namespace recovery target has a different owner")
	}
	token, code, err := w.kubernetes.Request(ctx, http.MethodPost, "/api/v1/namespaces/"+w.p.namespace+"/serviceaccounts/hypershell-namespace-allocation/token", kube.Object{"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest", "spec": kube.Object{"expirationSeconds": 600}})
	cancel()
	value := kube.String(token, "status", "token")
	if err != nil || code != 201 || value == "" {
		w.t.Fatal("namespace recovery token request failed")
	}
	file := filepath.Join(w.t.TempDir(), "allocator-token")
	if err := os.WriteFile(file, []byte(value), 0600); err != nil {
		w.t.Fatal(err)
	}
	client, err := kube.New(kube.Options{ServerURL: w.options.ServerURL, CAFile: w.options.CAFile, TokenFile: file})
	if err != nil {
		w.t.Fatal("namespace recovery client setup failed")
	}
	defer client.Close()
	oldUID := kube.String(beforeNamespace, "metadata", "uid")
	started := time.Now()
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	// Submit exactly one deletion with the observed UID. A retry must never
	// delete the replacement that a running controller has already created.
	_, code, err = client.Request(ctx, http.MethodDelete, namespacePath, kube.Object{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": kube.Object{"uid": oldUID}, "propagationPolicy": "Background"})
	cancel()
	if err != nil || (code != 200 && code != 202) {
		w.t.Fatal("workload namespace loss was not accepted", code)
	}
	deadline := time.Now().Add(180 * time.Second)
	newUID := ""
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		current, code, err := w.kubernetes.Request(ctx, http.MethodGet, namespacePath, nil)
		cancel()
		uid := kube.String(current, "metadata", "uid")
		if err == nil && code == 200 && uid != "" && uid != oldUID && kube.String(current, "metadata", "deletionTimestamp") == "" {
			newUID = uid
			break
		}
		if time.Now().After(deadline) {
			w.t.Fatal("running controllers did not replace the lost workload namespace")
		}
		time.Sleep(time.Second)
	}
	w.check(id)
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	err = allocator.RequireNamespace(ctx, "gateway", namespace, id)
	cancel()
	if err != nil || kube.String(read(namespacePath), "metadata", "uid") != newUID {
		w.t.Fatal("replacement namespace has an unstable identity or a different owner")
	}
	afterDeployment := read(deploymentPath)
	if kube.String(afterDeployment, "metadata", "uid") == kube.String(beforeDeployment, "metadata", "uid") {
		w.t.Fatal("namespace recovery did not replace the workload Deployment")
	}
	afterState := read(statePath)
	afterSource := read(statePath + "/secrets/openshell-gateway-state")
	if kube.String(afterState, "metadata", "uid") != kube.String(beforeState, "metadata", "uid") || kube.String(afterState, "metadata", "annotations", "hypershell.redhat.io/state-identity") != fingerprint || kube.String(afterSource, "metadata", "uid") != kube.String(beforeSource, "metadata", "uid") || !reflect.DeepEqual(afterSource["data"], beforeSource["data"]) {
		w.t.Fatal("workload namespace loss changed retained source state")
	}
	afterOptions, _ := w.sqlOptions(id)
	if !reflect.DeepEqual(options, afterOptions) || sqlIdentity(afterOptions) != beforeSQLObjects {
		w.t.Fatal("namespace recovery changed SQL credentials or recreated SQL objects")
	}
	afterSQL := w.checkSQLIsolation()
	for _, gatewayID := range w.gatewayIDs {
		before, after := beforeSQL[gatewayID], afterSQL[gatewayID]
		if gatewayID != id {
			currentOptions, _ := w.sqlOptions(gatewayID)
			if !reflect.DeepEqual(before, after) || !reflect.DeepEqual(otherOptions[gatewayID], currentOptions) || otherSQLObjects[gatewayID] != sqlIdentity(currentOptions) {
				w.t.Fatal("namespace recovery changed another Gateway's resources")
			}
			w.check(gatewayID)
		} else if before["source"] != after["source"] || before["server"] != after["server"] || before["credentials"] == after["credentials"] || before["keys"] == after["keys"] {
			w.t.Fatal("namespace recovery did not restore published resources from retained state")
		}
	}
	owner = w.identity.browserLogin(w.t, w.audience(id), "console-alice")
	deadline = time.Now().Add(60 * time.Second)
	for {
		after, err := w.call("GetProvider", owner, `{"name":"browser-provider"}`)
		if err == nil {
			if !proto.Equal(provider, after) {
				w.t.Fatal("namespace recovery changed encrypted provider data")
			}
			break
		}
		if (status.Code(err) != codes.Unavailable && status.Code(err) != codes.DeadlineExceeded) || time.Now().After(deadline) {
			w.t.Fatal("namespace recovery did not restore verified Gateway RPC", status.Code(err))
		}
		time.Sleep(time.Second)
	}
	checkViewer(owner)
	other := w.identity.browserLogin(w.t, w.audience(id), "console-bob")
	if _, err := w.call("GetProvider", other, `{"name":"browser-provider"}`); status.Code(err) != codes.PermissionDenied {
		w.t.Fatal("namespace recovery changed Gateway access rules")
	}
	w.checkAllocationAccess()
	if !reflect.DeepEqual(controllers, w.namespaceRecoveryControllers()) {
		w.t.Fatal("namespace recovery restarted a controller Pod")
	}
	ctx, cancel = context.WithTimeout(context.Background(), 10*time.Second)
	w.requireInstallationData(ctx)
	cancel()
	if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
		record := map[string]any{"gateway_id": id, "namespace": namespace, "old_namespace_uid": oldUID, "new_namespace_uid": newUID, "old_deployment_uid": kube.String(beforeDeployment, "metadata", "uid"), "new_deployment_uid": kube.String(afterDeployment, "metadata", "uid"), "state_namespace_uid": kube.String(afterState, "metadata", "uid"), "source_secret_uid": kube.String(afterSource, "metadata", "uid"), "source_data_unchanged": true, "database_and_role_oids_unchanged": true, "sql_credentials_unchanged": true, "provider_data_unchanged": true, "ungranted_rpc_denied": true, "viewer_membership_recovered": true, "viewer_lists_filtered": true, "viewer_writes_denied": true, "workspace_and_gateway_revocation_verified": true, "other_gateway_sql_unchanged": true, "controller_pods_unchanged": controllers, "seconds": time.Since(started).Seconds()}
		data, err := json.MarshalIndent(record, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(directory, "namespace-replacement.json"), append(data, '\n'), 0600) != nil {
			w.t.Fatal("cannot write namespace recovery evidence")
		}
	}
	w.t.Log("Running controllers replaced the lost workload namespace and Deployment; source keys, SQL database and roles, credentials, provider data, and access rules were preserved. The other Gateway's SQL state was unchanged")
}

func (w *browserGatewayWorkload) namespaceRecoveryControllers() map[string]string {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	page, code, err := w.kubernetes.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+w.p.namespace+"/pods?limit=16", nil)
	items, ok := kube.Nested(page, "items").([]any)
	if err != nil || code != 200 || !ok || len(items) > 16 || kube.String(page, "metadata", "continue") != "" || kube.String(page, "metadata", "resourceVersion") == "" {
		w.t.Fatal("namespace recovery controller inventory is incomplete")
	}
	result := map[string]string{}
	for _, value := range items {
		pod, ok := value.(map[string]any)
		if !ok {
			w.t.Fatal("namespace recovery controller inventory is invalid")
		}
		name := kube.String(pod, "spec", "serviceAccountName")
		switch name {
		case "hypershell-namespace-allocation", "hypershell-gateway-workload", "hypershell-gateway-identity":
		default:
			continue
		}
		if kube.String(pod, "metadata", "deletionTimestamp") != "" {
			continue
		}
		uid := kube.String(pod, "metadata", "uid")
		containers, ok := kube.Nested(pod, "status", "containerStatuses").([]any)
		if uid == "" || result[name] != "" || kube.String(pod, "status", "phase") != "Running" || !ok || len(containers) != 1 {
			w.t.Fatal("namespace recovery requires one running Pod per controller")
		}
		container, ok := containers[0].(map[string]any)
		if !ok || container["ready"] != true || container["restartCount"] != json.Number("0") {
			w.t.Fatal("namespace recovery controller is not ready or has restarted")
		}
		result[name] = uid
	}
	if len(result) != 3 {
		w.t.Fatal("namespace recovery requires all three controllers")
	}
	return result
}
