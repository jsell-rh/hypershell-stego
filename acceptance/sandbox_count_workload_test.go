package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/metadata"
)

func startSandboxCountWorkflow(t *testing.T, k *kubeFixture, httpAddress, rpcAddress string, tlsIdentity testIdentity, controller, owner, cluster string, gateway httpapi.Gateway) (func(int32), func()) {
	t.Helper()
	name := k.options.ClusterIssuer + "-counts"
	ns := k.options.ClusterIssuer
	k.apply(t, map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": name, "namespace": ns}},
		map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": map[string]any{"name": name}, "rules": []any{map[string]any{"apiGroups": []string{""}, "resources": []string{"pods"}, "verbs": []string{"get", "list", "watch"}}}},
		map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": map[string]any{"name": name}, "roleRef": map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": name}, "subjects": []any{map[string]any{"kind": "ServiceAccount", "name": name, "namespace": ns}}})
	t.Cleanup(func() { k.must(t, "", "delete", "clusterrole,clusterrolebinding", name, "--ignore-not-found=true") })
	copy := *k
	copy.options.TokenFile = filepath.Join(t.TempDir(), "pod-reader-token")
	if err := os.WriteFile(copy.options.TokenFile, k.must(t, "", "-n", ns, "create", "token", name, "--duration=1h"), 0600); err != nil {
		t.Fatal(err)
	}
	// The same read-only role is used by the actual controller process.
	if output, err := k.command(context.Background(), "", "auth", "can-i", "create", "pods", "--as=system:serviceaccount:"+ns+":"+name); err == nil || string(output) != "no\n" {
		t.Fatal("count account can change Pods", string(output), err)
	}
	binary := buildProgram(t, "./cmd/sandbox-count-controller")
	settings := []string{"HYPERSHELL_MANAGED_CLUSTER_ID=" + cluster, "HYPERSHELL_SANDBOX_COUNT_RESYNC=1s"}
	stop, logs := startDatabaseController(t, binary, &copy, rpcAddress, tlsIdentity.config.CAFile, controller, settings...)
	t.Cleanup(func() { stop() })
	client, connection := grpcClient(t, rpcAddress, tlsIdentity)
	t.Cleanup(func() { connection.Close() })
	check := func(want int32) {
		t.Helper()
		deadline := time.Now().Add(20 * time.Second)
		for {
			code, data := requestJSON(t, "GET", httpAddress+"/api/hypershell/v1/gateways/"+gateway.ID, owner, nil)
			var row httpapi.Gateway
			ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+owner)), 5*time.Second)
			got, err := client.GetGateway(ctx, &pb.GetGatewayRequest{Id: gateway.ID})
			cancel()
			if code == 200 && json.Unmarshal(data, &row) == nil && row.ActiveSandboxCount != nil && *row.ActiveSandboxCount == want && err == nil && got.Gateway.ActiveSandboxCount != nil && got.Gateway.GetActiveSandboxCount() == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("sandbox count %d missing through REST or gRPC: %s %v %v\n%s", want, data, got, err, logs())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	recover := func() {
		t.Helper()
		check(1)
		// A stale value must be repaired without another Pod event.
		write := func() {
			ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+controller)), 5*time.Second)
			defer cancel()
			if _, err := client.SetActiveSandboxCount(ctx, &pb.SetActiveSandboxCountRequest{Namespace: gateway.Namespace, Count: 9}); err != nil {
				t.Fatal(err)
			}
		}
		write()
		check(1)
		stop()
		write()
		stop, logs = startDatabaseController(t, binary, &copy, rpcAddress, tlsIdentity.config.CAFile, controller, settings...)
		check(1)
		t.Log("Pod count recovered from drift and controller restart through REST and gRPC")
	}
	return check, recover
}
