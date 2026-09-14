package acceptance

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"github.com/jsell-rh/hypershell-stego/internal/sandboxcount"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"google.golang.org/grpc/metadata"
)

type countNamespace struct {
	owner, uid     string
	pods           []kube.Object
	denied         bool
	denials, opens int
	watches        map[chan kube.Object]bool
}
type countKubernetes struct {
	mu         sync.Mutex
	namespaces map[string]*countNamespace
	allocator  *allocation.Allocator
	invalid    int
}

func (k *countKubernetes) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	k.mu.Lock()
	parts := strings.Split(r.URL.Path, "/")
	if r.Method != "GET" || r.Header.Get("Authorization") != "Bearer count-fixture" || len(parts) < 5 || len(parts) > 6 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "namespaces" {
		k.invalid++
		k.mu.Unlock()
		w.WriteHeader(403)
		return
	}
	ns := k.namespaces[parts[4]]
	if ns == nil {
		k.mu.Unlock()
		w.WriteHeader(404)
		return
	}
	if len(parts) == 5 {
		result := kube.Object{"metadata": kube.Object{"name": parts[4], "uid": ns.uid, "resourceVersion": "1", "labels": kube.Object{allocation.MarkerLabel: k.allocator.Marker(), allocation.ProfileLabel: "gateway", "hypershell.redhat.io/gateway-id": ns.owner, "app.kubernetes.io/managed-by": "hypershell-gateway-controller"}}}
		k.mu.Unlock()
		_ = json.NewEncoder(w).Encode(result)
		return
	}
	if parts[5] != "pods" || r.URL.Query().Get("labelSelector") != sandboxcount.SandboxLabel {
		k.invalid++
		k.mu.Unlock()
		w.WriteHeader(403)
		return
	}
	if ns.denied {
		ns.denials++
		k.mu.Unlock()
		w.WriteHeader(403)
		return
	}
	if r.URL.Query().Get("watch") != "true" {
		pods := append([]kube.Object{}, ns.pods...)
		k.mu.Unlock()
		_ = json.NewEncoder(w).Encode(kube.Object{"metadata": kube.Object{"resourceVersion": "1"}, "items": pods})
		return
	}
	events := make(chan kube.Object, 8)
	ns.watches[events] = true
	ns.opens++
	k.mu.Unlock()
	defer func() { k.mu.Lock(); delete(ns.watches, events); k.mu.Unlock() }()
	_ = json.NewEncoder(w).Encode(kube.Object{"type": "BOOKMARK", "object": kube.Object{"metadata": kube.Object{"resourceVersion": "1"}}})
	w.(http.Flusher).Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case event := <-events:
			if json.NewEncoder(w).Encode(event) != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}
}
func (k *countKubernetes) add(row *pb.Gateway, denied bool) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.namespaces[row.Namespace] = &countNamespace{owner: row.Metadata.Id, uid: "uid-" + row.Namespace, denied: denied, watches: map[chan kube.Object]bool{}}
}
func (k *countKubernetes) pod(t *testing.T, ns, uid string) {
	t.Helper()
	k.mu.Lock()
	defer k.mu.Unlock()
	object := kube.Object{"metadata": kube.Object{"namespace": ns, "uid": uid, "resourceVersion": "2", "labels": kube.Object{sandboxcount.SandboxLabel: "sandbox"}}, "status": kube.Object{"phase": "Running"}}
	target := k.namespaces[ns]
	target.pods = append(target.pods, object)
	for stream := range target.watches {
		select {
		case stream <- kube.Object{"type": "ADDED", "object": object}:
		default:
			t.Fatal("count fixture event buffer is full")
		}
	}
}

// The Kubernetes endpoint is a TLS protocol fixture. This test proves the
// generated process and API behavior; live Kubernetes RBAC needs its own gate.
func TestNamespaceCountWorkflowThroughGeneratedWorker(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "DATABASE_PROVIDER=cnpg", "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	settings = withControllerWriteGrants(t, settings, writeGrant("controller", "observe.sandbox-count", f.cluster))
	binary := buildApplication(t)
	stop, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	client, connection := grpcClient(t, rpcAddress, tlsIdentity)
	defer connection.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner := token(t, key, "owner", "gateway:creator")
	controller := token(t, key, "controller")
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	create := func(name string) *pb.Gateway {
		t.Helper()
		created, err := client.CreateGateway(call(owner), &pb.CreateGatewayRequest{Name: name, ClusterId: f.cluster, ReleaseId: f.release})
		if err != nil {
			t.Fatal(err)
		}
		readGatewayEvent(t, consumer, created.Gateway.Metadata.Id, "Create", "gateway.created")
		return created.Gateway
	}
	one, two := create("namespace-one"), create("namespace-two")
	provider := &countKubernetes{namespaces: map[string]*countNamespace{}}
	provider.add(one, false)
	provider.add(two, false)
	server := httptest.NewTLSServer(provider)
	defer server.Close()
	files := t.TempDir()
	ca, tokenFile := filepath.Join(files, "ca.pem"), filepath.Join(files, "kube-token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenFile, []byte("count-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	k := &kubeFixture{options: databasecontroller.KubernetesOptions{ServerURL: server.URL, CAFile: ca, TokenFile: tokenFile, ControlNamespace: "count-control"}}
	proofClient, err := kube.New(kube.Options{ServerURL: server.URL, CAFile: ca, TokenFile: tokenFile})
	if err != nil {
		t.Fatal(err)
	}
	defer proofClient.Close()
	provider.allocator, err = allocation.New(proofClient, "count-control")
	if err != nil {
		t.Fatal(err)
	}
	worker := buildProgram(t, "./out/deploy/workers/sandbox-count")
	workerSettings := []string{"HYPERSHELL_CONTROL_NAMESPACE=count-control", "HYPERSHELL_MANAGED_CLUSTER_ID=" + f.cluster, "HYPERSHELL_SANDBOX_COUNT_RESYNC=1s", "HYPERSHELL_SANDBOX_COUNT_WATCH_LIMIT=4"}
	stopWorker, logs := startDatabaseController(t, worker, k, rpcAddress, tlsIdentity.config.CAFile, controller, workerSettings...)
	defer func() { stopWorker() }()
	readCount := func(row *pb.Gateway, want int32) {
		t.Helper()
		deadline := time.Now().Add(12 * time.Second)
		for {
			code, data := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways/"+row.Metadata.Id, owner, nil)
			var rest httpapi.Gateway
			rpc, err := client.GetGateway(call(owner), &pb.GetGatewayRequest{Id: row.Metadata.Id})
			if code == 200 && json.Unmarshal(data, &rest) == nil && rest.ActiveSandboxCount != nil && *rest.ActiveSandboxCount == want && err == nil && rpc.Gateway.GetActiveSandboxCount() == want {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("namespace count %d missing: %s %v\n%s", want, data, err, logs())
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	wait := func(check func() bool) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatalf("namespace watch did not converge\n%s", logs())
			}
			time.Sleep(25 * time.Millisecond)
		}
	}
	wait(func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.namespaces[one.Namespace].opens > 0 && provider.namespaces[two.Namespace].opens > 0
	})
	readCount(one, 0)
	readCount(two, 0)
	provider.pod(t, one.Namespace, "one-pod")
	readCount(one, 1)
	readGatewayEvent(t, consumer, one.Metadata.Id, "Update", "gateway.updated")
	provider.pod(t, two.Namespace, "two-pod")
	readCount(two, 1)
	readGatewayEvent(t, consumer, two.Metadata.Id, "Update", "gateway.updated")
	// A denied baseline must preserve the API value, including through rescan.
	three := create("namespace-three")
	provider.add(three, true)
	if _, err := client.SetActiveSandboxCount(call(controller), &pb.SetActiveSandboxCountRequest{Namespace: three.Namespace, Count: 9}); err != nil {
		t.Fatal(err)
	}
	readGatewayEvent(t, consumer, three.Metadata.Id, "Update", "gateway.updated")
	wait(func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.namespaces[three.Namespace].denials >= 2
	})
	readCount(three, 9)
	provider.mu.Lock()
	provider.namespaces[three.Namespace].denied = false
	provider.mu.Unlock()
	readCount(three, 0)
	readGatewayEvent(t, consumer, three.Metadata.Id, "Update", "gateway.updated")
	// No Pod event tells the old watch about this namespace replacement.
	provider.mu.Lock()
	provider.namespaces[two.Namespace].uid = "new-namespace-uid"
	provider.namespaces[two.Namespace].pods = nil
	provider.mu.Unlock()
	readCount(two, 0)
	readGatewayEvent(t, consumer, two.Metadata.Id, "Update", "gateway.updated")
	wait(func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return provider.namespaces[two.Namespace].opens >= 2
	})
	stopWorker()
	stop()
	provider.mu.Lock()
	provider.namespaces[one.Namespace].pods = nil
	provider.mu.Unlock()
	stop, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	connection.Close()
	client, connection = grpcClient(t, rpcAddress, tlsIdentity)
	defer connection.Close()
	stopWorker, logs = startDatabaseController(t, worker, k, rpcAddress, tlsIdentity.config.CAFile, controller, workerSettings...)
	readCount(one, 0)
	readGatewayEvent(t, consumer, one.Metadata.Id, "Update", "gateway.updated")
	if code, _ := requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+three.Metadata.Id, owner, nil); code != 204 {
		t.Fatal("Gateway deletion failed", code)
	}
	readGatewayEvent(t, consumer, three.Metadata.Id, "Delete", "gateway.deleted")
	wait(func() bool {
		provider.mu.Lock()
		defer provider.mu.Unlock()
		return len(provider.namespaces[three.Namespace].watches) == 0
	})
	provider.mu.Lock()
	invalid := provider.invalid
	provider.mu.Unlock()
	if invalid != 0 {
		t.Fatal("worker requested an unscoped or invalid collection", invalid)
	}
	t.Log("Scoped Pod baselines, denied reads, namespace replacement, events, and API/worker restart passed through REST and gRPC")
}
