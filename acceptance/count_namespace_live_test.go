package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/internal/sandboxcount"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Run through scripts/check-count-namespaces.py. Each Kubernetes client uses
// its generated worker role. The Job has no ambient API credential.
func TestNamespaceCountWithLiveKubernetes(t *testing.T) {
	if os.Getenv("STEGO_TEST_NAMESPACE_COUNT_LIVE") != "1" {
		t.Skip("requires an isolated cluster Job and generated worker credentials")
	}
	control := os.Getenv("STEGO_TEST_NAMESPACE")
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	options := kube.Options{ServerURL: os.Getenv("STEGO_TEST_KUBERNETES_URL"), CAFile: "/count-credentials/ca.crt"}
	newClient := func(name string) *kube.Client {
		t.Helper()
		o := options
		o.TokenFile = "/count-credentials/" + name
		c, err := kube.New(o)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	allocatorClient, workloadClient, countClient := newClient("allocator"), newClient("workload"), newClient("count")
	defer allocatorClient.Close()
	defer workloadClient.Close()
	defer countClient.Close()
	a, err := allocation.New(allocatorClient, control)
	if err != nil {
		t.Fatal(err)
	}
	var allocated []*pb.Gateway
	sandboxNamespace := func(row *pb.Gateway) string {
		t.Helper()
		sandbox, err := gatewayworkload.SandboxNamespace(row.Metadata.Id)
		if err != nil {
			t.Fatal(err)
		}
		return sandbox
	}
	remove := func(row *pb.Gateway) error {
		cleanup, done := context.WithTimeout(context.Background(), 100*time.Second)
		defer done()
		for cleanup.Err() == nil {
			gone, gatewayErr := a.Delete(cleanup, "gateway", row.Namespace, row.Metadata.Id)
			if gatewayErr != nil && !errors.Is(gatewayErr, allocation.ErrPending) {
				return gatewayErr
			}
			absent, sandboxErr := a.Delete(cleanup, "sandbox", sandboxNamespace(row), row.Metadata.Id)
			if sandboxErr != nil && !errors.Is(sandboxErr, allocation.ErrPending) {
				return sandboxErr
			}
			if gone && absent {
				return nil
			}
			time.Sleep(time.Second)
		}
		return cleanup.Err()
	}
	defer func() {
		for _, row := range allocated {
			if err := remove(row); err != nil {
				t.Errorf("namespace cleanup %s: %v", row.Namespace, err)
			}
		}
	}()
	ensure := func(row *pb.Gateway) {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			gatewayErr := a.Ensure(ctx, "gateway", row.Namespace, row.Metadata.Id)
			sandboxErr := a.Ensure(ctx, "sandbox", sandboxNamespace(row), row.Metadata.Id)
			if gatewayErr == nil && sandboxErr == nil {
				return
			}
			if time.Now().After(deadline) || (!errors.Is(gatewayErr, allocation.ErrPending) && gatewayErr != nil) || (!errors.Is(sandboxErr, allocation.ErrPending) && sandboxErr != nil) {
				t.Fatal("allocate Gateway and Sandbox namespaces:", gatewayErr, sandboxErr)
			}
			time.Sleep(time.Second)
		}
	}
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	settings = withControllerWriteGrants(t, settings, writeGrant("controller", "observe.sandbox-count", f.cluster))
	binary := buildApplication(t)
	stop, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	client, connection := grpcClient(t, rpcAddress, tlsIdentity)
	defer connection.Close()
	owner, controller := token(t, key, "owner", "gateway:creator"), token(t, key, "controller")
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	create := func(name string) *pb.Gateway {
		t.Helper()
		created, err := client.CreateGateway(call(owner), &pb.CreateGatewayRequest{Name: name, ClusterId: f.cluster, ReleaseId: f.release})
		if err != nil {
			t.Fatal(err)
		}
		row := created.Gateway
		readGatewayEvent(t, consumer, row.Metadata.Id, "Create", "gateway.created")
		allocated = append(allocated, row)
		t.Logf("Allocated target: %s %s", row.Namespace, row.Metadata.Id)
		ensure(row)
		return row
	}
	one, two := create("live-one"), create("live-two")
	// A nonzero stored value proves that the first empty list was consumed.
	if _, err := client.SetActiveSandboxCount(call(controller), &pb.SetActiveSandboxCountRequest{Namespace: one.Namespace, Count: 9}); err != nil {
		t.Fatal(err)
	}
	readGatewayEvent(t, consumer, one.Metadata.Id, "Update", "gateway.updated")
	if _, err := client.SetActiveSandboxCount(call(owner), &pb.SetActiveSandboxCountRequest{Namespace: one.Namespace, Count: 0}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("owner count write was not denied", err)
	}
	k := &kubeFixture{options: gatewayworkload.Options{ServerURL: options.ServerURL, CAFile: options.CAFile, TokenFile: "/count-credentials/count", ControlNamespace: control}}
	worker := buildProgram(t, "./out/deploy/workers/sandbox-count")
	workerSettings := []string{"HYPERSHELL_CONTROL_NAMESPACE=" + control, "HYPERSHELL_MANAGED_CLUSTER_ID=" + f.cluster, "HYPERSHELL_SANDBOX_COUNT_RESYNC=1s", "HYPERSHELL_SANDBOX_COUNT_WATCH_LIMIT=4"}
	stopWorker, logs := startDatabaseController(t, worker, k, rpcAddress, tlsIdentity.config.CAFile, controller, workerSettings...)
	defer func() { stopWorker() }()
	readCount := func(row *pb.Gateway, want int32) {
		t.Helper()
		deadline := time.Now().Add(35 * time.Second)
		for {
			code, data := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways/"+row.Metadata.Id, owner, nil)
			var rest gatewayResponse
			rpc, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: row.Metadata.Id})
			if code == 200 && json.Unmarshal(data, &rest) == nil && rest.ActiveSandboxCount != nil && *rest.ActiveSandboxCount == want && err == nil && rpc.Gateway.GetActiveSandboxCount() == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("count %d did not reach REST and gRPC: %s %v\n%s", want, data, err, logs())
			}
			time.Sleep(200 * time.Millisecond)
		}
	}
	readCount(one, 0)
	readGatewayEvent(t, consumer, one.Metadata.Id, "Update", "gateway.updated")
	readCount(two, 0)
	// Check the actual authorizer. An API error is never a denied result.
	review := func(verb, group, resource, namespace string, want bool) {
		t.Helper()
		body := kube.Object{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "spec": kube.Object{"resourceAttributes": kube.Object{"verb": verb, "group": group, "resource": resource, "namespace": namespace}}}
		deadline := time.Now().Add(15 * time.Second)
		for {
			result, code, err := countClient.Request(ctx, http.MethodPost, "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", body)
			allowed, ok := kube.Nested(result, "status", "allowed").(bool)
			if err != nil || code != 201 || !ok || kube.String(result, "status", "evaluationError") != "" {
				t.Fatalf("permission review failed: %d %v %v", code, result, err)
			}
			if allowed == want {
				t.Logf("Permission: %s %s/%s namespace=%s allowed=%t", verb, group, resource, namespace, allowed)
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("permission %s %s/%s namespace %s did not become %t: %v", verb, group, resource, namespace, want, result)
			}
			// The authorizer must observe the binding change before the next step.
			time.Sleep(200 * time.Millisecond)
		}
	}
	for _, verb := range []string{"get", "list", "watch"} {
		review(verb, "", "pods", sandboxNamespace(one), true)
	}
	review("list", "", "pods", "", false)
	review("list", "", "pods", control, false)
	review("get", "", "secrets", sandboxNamespace(one), false)
	review("create", "apps", "deployments", sandboxNamespace(one), false)
	review("create", "rbac.authorization.k8s.io", "rolebindings", sandboxNamespace(one), false)
	review("patch", "", "namespaces", "", false)
	review("get", "", "namespaces", "", true)
	for _, path := range []string{"/api/v1/pods", "/api/v1/namespaces/" + control + "/pods", "/api/v1/namespaces/" + sandboxNamespace(one) + "/secrets/probe"} {
		_, code, err := countClient.Request(ctx, http.MethodGet, path, nil)
		var api *kube.APIError
		if code != 403 || !errors.As(err, &api) || api.StatusCode != 403 {
			t.Fatal("expected direct denial", path, code, err)
		}
	}
	// The workload role can create bare Pods only in the sandbox namespace, and
	// the sandbox admission policy fixes their shape: an agent container, the
	// allocated service account, no token mount, and no runtime class.
	pods := func(row *pb.Gateway, count int) {
		t.Helper()
		namespace := sandboxNamespace(row)
		account, err := a.ServiceAccountName("sandbox", namespace, row.Metadata.Id, "sandbox")
		if err != nil {
			t.Fatal(err)
		}
		collection := "/api/v1/namespaces/" + namespace + "/pods"
		deadline := time.Now().Add(90 * time.Second)
		for i := 0; i < count; i++ {
			name := fmt.Sprintf("count-fixture-%d", i)
			desired := kube.Object{
				"apiVersion": "v1", "kind": "Pod",
				"metadata": kube.Object{"name": name, "namespace": namespace, "labels": kube.Object{sandboxcount.SandboxLabel: "fixture", "hypershell.redhat.io/gateway-id": row.Metadata.Id}},
				"spec": kube.Object{
					"automountServiceAccountToken": false, "restartPolicy": "Never", "terminationGracePeriodSeconds": 1,
					"serviceAccountName": account,
					"securityContext":    kube.Object{"runAsNonRoot": true, "runAsUser": 1000, "seccompProfile": kube.Object{"type": "RuntimeDefault"}},
					"containers": []any{kube.Object{
						"name": "agent", "image": os.Getenv("STEGO_TEST_IDLE_IMAGE"),
						"command": []any{"/bin/sh", "-c", "sleep 900"},
						"resources": kube.Object{
							"requests": kube.Object{"cpu": "10m", "memory": "16Mi", "ephemeral-storage": "16Mi"},
							"limits":   kube.Object{"cpu": "10m", "memory": "16Mi", "ephemeral-storage": "16Mi"},
						},
						"securityContext": kube.Object{
							"runAsNonRoot": true, "runAsUser": 1000, "allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true,
							"capabilities": kube.Object{"drop": []any{"ALL"}},
						},
					}},
				},
			}
			created, code, err := workloadClient.Request(ctx, http.MethodPost, collection, desired)
			if err != nil || code != 201 {
				t.Fatal("create bounded sandbox Pod", code, err, created)
			}
			path := collection + "/" + name
			for {
				object, code, err := workloadClient.Request(ctx, http.MethodGet, path, nil)
				if err != nil {
					t.Fatal(err)
				}
				if phase := kube.String(object, "status", "phase"); code == 200 && (phase == "Pending" || phase == "Running") {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("bounded Pod %s did not exist: %v", name, object["status"])
				}
				time.Sleep(time.Second)
			}
		}
	}
	pods(one, 1)
	readCount(one, 1)
	readGatewayEvent(t, consumer, one.Metadata.Id, "Update", "gateway.updated")
	pods(two, 1)
	readCount(two, 1)
	readGatewayEvent(t, consumer, two.Metadata.Id, "Update", "gateway.updated")
	three := create("live-added")
	pods(three, 1)
	readCount(three, 1)
	readGatewayEvent(t, consumer, three.Metadata.Id, "Update", "gateway.updated")
	// Remove the declared count binding, with live ownership and UID checks.
	ownerLabels := kube.Owner{allocation.MarkerLabel: a.Marker(), allocation.ProfileLabel: "sandbox", "hypershell.redhat.io/gateway-id": one.Metadata.Id, "app.kubernetes.io/managed-by": "hypershell-gateway-controller"}
	bindingCollection := "/apis/rbac.authorization.k8s.io/v1/namespaces/" + sandboxNamespace(one) + "/rolebindings"
	query := url.Values{"limit": {"64"}, "labelSelector": {allocation.MarkerLabel + "=" + a.Marker()}}
	page, code, err := allocatorClient.Request(ctx, http.MethodGet, bindingCollection+"?"+query.Encode(), nil)
	if err != nil || code != http.StatusOK {
		t.Fatal("count binding snapshot", code, err)
	}
	binding, err := selectCountBinding(page, ownerLabels, control, sandboxNamespace(one))
	if err != nil {
		t.Fatal("count binding identity", err)
	}
	bindingPath := bindingCollection + "/" + url.PathEscape(kube.String(binding, "metadata", "name"))
	// Protected bindings use background deletion. A garbage collector cannot
	// update their finalizers through the allocator-only admission policy.
	if _, _, err := allocatorClient.Request(ctx, http.MethodDelete, bindingPath, kube.Object{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": kube.Object{"uid": kube.String(binding, "metadata", "uid"), "resourceVersion": kube.String(binding, "metadata", "resourceVersion")}, "propagationPolicy": "Background"}); err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(15 * time.Second); ; {
		current, code, err := allocatorClient.Request(ctx, http.MethodGet, bindingPath, nil)
		if err != nil {
			t.Fatal(err)
		}
		if code == 404 {
			break
		}
		if kube.String(current, "metadata", "uid") != kube.String(binding, "metadata", "uid") {
			t.Fatal("count binding changed during removal")
		}
		if time.Now().After(deadline) {
			t.Fatal("count binding deletion did not complete")
		}
		time.Sleep(200 * time.Millisecond)
	}
	review("list", "", "pods", sandboxNamespace(one), false)
	stopWorker()
	stopWorker, logs = startDatabaseController(t, worker, k, rpcAddress, tlsIdentity.config.CAFile, controller, workerSettings...)
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(logs(), "Sandbox Pod watch needs a new assignment check") {
		if time.Now().After(deadline) {
			t.Fatalf("denied namespace did not reset watch: %s", logs())
		}
		time.Sleep(200 * time.Millisecond)
	}
	pods(one, 2)
	// Another namespace must still make progress while this read is denied.
	pods(two, 2)
	readCount(two, 2)
	readGatewayEvent(t, consumer, two.Metadata.Id, "Update", "gateway.updated")
	readCount(one, 1)
	ensure(one)
	readCount(one, 2)
	readGatewayEvent(t, consumer, one.Metadata.Id, "Update", "gateway.updated")
	// Reuse the name with a new Kubernetes UID while its API row stays live.
	oldUID, err := a.NamespaceUID(ctx, "gateway", three.Namespace, three.Metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	oldSandboxUID, err := a.NamespaceUID(ctx, "sandbox", sandboxNamespace(three), three.Metadata.Id)
	if err != nil {
		t.Fatal(err)
	}
	if err := remove(three); err != nil {
		t.Fatal(err)
	}
	ensure(three)
	newUID, err := a.NamespaceUID(ctx, "gateway", three.Namespace, three.Metadata.Id)
	if err != nil || newUID == oldUID {
		t.Fatal("namespace UID did not change", err)
	}
	newSandboxUID, err := a.NamespaceUID(ctx, "sandbox", sandboxNamespace(three), three.Metadata.Id)
	if err != nil || newSandboxUID == oldSandboxUID {
		t.Fatal("sandbox namespace UID did not change", err)
	}
	readCount(three, 0)
	readGatewayEvent(t, consumer, three.Metadata.Id, "Update", "gateway.updated")
	pods(three, 1)
	readCount(three, 1)
	readGatewayEvent(t, consumer, three.Metadata.Id, "Update", "gateway.updated")
	stopWorker()
	if _, err := client.SetActiveSandboxCount(call(controller), &pb.SetActiveSandboxCountRequest{Namespace: one.Namespace, Count: 9}); err != nil {
		t.Fatal(err)
	}
	readGatewayEvent(t, consumer, one.Metadata.Id, "Update", "gateway.updated")
	stop()
	stop, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	connection.Close()
	client, connection = grpcClient(t, rpcAddress, tlsIdentity)
	defer connection.Close()
	stopWorker, logs = startDatabaseController(t, worker, k, rpcAddress, tlsIdentity.config.CAFile, controller, workerSettings...)
	readCount(one, 2)
	readGatewayEvent(t, consumer, one.Metadata.Id, "Update", "gateway.updated")
	if code, _ := requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+three.Metadata.Id, owner, nil); code != 202 {
		t.Fatal("Gateway deletion failed", code)
	}
	readGatewayEvent(t, consumer, three.Metadata.Id, "Update", "gateway.updated")
	if err := remove(three); err != nil {
		t.Fatal(err)
	}
	readCount(two, 2)
	t.Log("Live Pods, generated permissions, denied reads, namespace UID replacement, events, REST, gRPC, and API/worker restart passed")
}
