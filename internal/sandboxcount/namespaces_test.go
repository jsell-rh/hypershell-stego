package sandboxcount

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"github.com/segmentio/ksuid"
	"google.golang.org/protobuf/proto"
)

type countWrite struct {
	Namespace, ClusterId string
	Count                int32
}

type openedNamespace struct {
	namespace string
	apply     func(kube.Change) error
	stopped   chan struct{}
}
type namespacePods struct{ opened chan openedNamespace }

func (s *namespacePods) WatchLimit() int { return 16 }
func (s *namespacePods) Observe(ctx context.Context, c kube.Collection, apply func(kube.Change) error) error {
	parts := strings.Split(c.Path, "/")
	if len(parts) != 6 || parts[1] != "api" || parts[2] != "v1" || parts[3] != "namespaces" || parts[5] != "pods" || c.LabelSelector != SandboxLabel {
		return errors.New("count watch is not scoped")
	}
	stopped := make(chan struct{})
	defer close(stopped)
	return serveChanges(ctx, apply, func(send func(kube.Change) error) {
		select {
		case s.opened <- openedNamespace{parts[4], send, stopped}:
		case <-ctx.Done():
		}
	})
}

type namespaceProof struct {
	mu      sync.Mutex
	uids    map[string]string
	calls   map[string]int
	fail    bool
	sandbox bool
}

func (p *namespaceProof) NamespaceUID(ctx context.Context, profile, name, id string) (string, error) {
	if p.sandbox {
		expected, err := gatewayworkload.SandboxNamespace(id)
		if err != nil || profile != "sandbox" || name != expected {
			return "", errors.New("wrong Sandbox allocation request")
		}
	} else if _, err := (namespaceFixture{}).NamespaceUID(ctx, profile, name, id); err != nil {
		return "", err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls[name]++
	if p.fail {
		return "", errors.New("namespace identity changed")
	}
	if p.uids[name] == "" {
		return "", allocation.ErrPending
	}
	return p.uids[name], nil
}
func gateway(cluster string) *pb.Gateway {
	id := ksuid.New().String()
	ns, _ := gatewayworkload.Namespace(id)
	return &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster}
}
func takeNamespace[T any](t *testing.T, ch <-chan T) T {
	t.Helper()
	select {
	case value := <-ch:
		return value
	case <-time.After(4 * time.Second):
		t.Fatal("namespace count check timed out")
		var zero T
		return zero
	}
}

func TestCountWatchesFollowVerifiedNamespaces(t *testing.T) {
	cluster := ksuid.New().String()
	one, two, three, foreign := gateway(cluster), gateway(cluster), gateway(cluster), gateway(ksuid.New().String())
	// Display text cannot stop a live namespace watch.
	one.Phase = proto.String("Deleting")
	api := &apiFixture{rows: []*pb.Gateway{one, two, three, foreign}, deleted: map[string]bool{}}
	proof := &namespaceProof{uids: map[string]string{one.Namespace: "uid-one", two.Namespace: "uid-two"}, calls: map[string]int{}}
	source := &namespacePods{opened: make(chan openedNamespace, 16)}
	writes := make(chan countWrite, 128)
	c, err := New(source, proof, api, &writerFixture{state: api, write: func(r *control.SetObservedSandboxCountRequest) error {
		writes <- countWrite{Namespace: r.Namespace, ClusterId: r.ClusterId, Count: r.Count}
		return nil
	}}, cluster, time.Second, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() { cancel(); takeNamespace(t, done) })
	opened := map[string]openedNamespace{}
	for range 2 {
		watch := takeNamespace(t, source.opened)
		if watch.namespace != one.Namespace && watch.namespace != two.Namespace {
			t.Fatal("unassigned namespace was watched", watch.namespace)
		}
		opened[watch.namespace] = watch
		if err := watch.apply(kube.Change{Type: "REPLACE", Objects: []kube.Object{record("pod-"+watch.namespace, watch.namespace, "Running", "1")}}); err != nil {
			t.Fatal(err)
		}
	}
	observed := map[string]int32{}
	waitCount := func(ns string, want int32) {
		t.Helper()
		for {
			write := takeNamespace(t, writes)
			if write.ClusterId != cluster || write.Namespace == foreign.Namespace {
				t.Fatal("count write escaped its cluster")
			}
			observed[write.Namespace] = write.Count
			if write.Namespace == ns && write.Count == want {
				return
			}
		}
	}
	for len(observed) < 2 {
		write := takeNamespace(t, writes)
		if write.Count != 1 || (write.Namespace != one.Namespace && write.Namespace != two.Namespace) {
			t.Fatal("count before namespace baseline", write.Namespace, write.Count)
		}
		observed[write.Namespace] = write.Count
	}
	proof.mu.Lock()
	if proof.calls[foreign.Namespace] != 0 {
		t.Error("foreign cluster allocation was read")
	}
	proof.uids[three.Namespace] = "uid-three"
	proof.mu.Unlock()
	third := takeNamespace(t, source.opened)
	if third.namespace != three.Namespace {
		t.Fatal("pending namespace was not added", third.namespace)
	}
	if err := third.apply(kube.Change{Type: "REPLACE"}); err != nil {
		t.Fatal(err)
	}
	waitCount(three.Namespace, 0)
	// Reset one namespace while another receives a new active Pod.
	if err := opened[one.Namespace].apply(kube.Change{Type: "RESET"}); err != nil {
		t.Fatal(err)
	}
	if err := opened[two.Namespace].apply(kube.Change{Type: "ADDED", Object: record("second", two.Namespace, "Pending", "2")}); err != nil {
		t.Fatal(err)
	}
	waitCount(two.Namespace, 2)
	if err := opened[one.Namespace].apply(kube.Change{Type: "REPLACE"}); err != nil {
		t.Fatal(err)
	}
	waitCount(one.Namespace, 0)
	api.mu.Lock()
	// Keep the pending Gateway in the public list until finalization.
	api.deleted[two.GetMetadata().GetId()] = true
	api.mu.Unlock()
	takeNamespace(t, opened[two.Namespace].stopped)
	if err := opened[two.Namespace].apply(kube.Change{Type: "ADDED", Object: record("late", two.Namespace, "Running", "3")}); err == nil {
		t.Fatal("removed watch accepted a late event")
	}
	proof.mu.Lock()
	proof.uids[one.Namespace] = "replacement-uid"
	proof.mu.Unlock()
	replacement := takeNamespace(t, source.opened)
	if replacement.namespace != one.Namespace {
		t.Fatal("wrong namespace replaced")
	}
	takeNamespace(t, opened[one.Namespace].stopped)
	if err := replacement.apply(kube.Change{Type: "REPLACE", Objects: []kube.Object{record("new-pod", one.Namespace, "Running", "4")}}); err != nil {
		t.Fatal(err)
	}
	waitCount(one.Namespace, 1)
	if err := opened[one.Namespace].apply(kube.Change{Type: "ADDED", Object: record("old", one.Namespace, "Running", "5")}); err == nil {
		t.Fatal("old namespace identity accepted an event")
	}
}

func TestCountStopsWhenAllocationIdentityCannotBeVerified(t *testing.T) {
	cluster := ksuid.New().String()
	row := gateway(cluster)
	api := &apiFixture{rows: []*pb.Gateway{row}}
	proof := &namespaceProof{uids: map[string]string{row.Namespace: "uid-one"}, calls: map[string]int{}}
	source := &namespacePods{opened: make(chan openedNamespace, 1)}
	c, err := New(source, proof, api, &writerFixture{state: api, write: func(*control.SetObservedSandboxCountRequest) error { return nil }}, cluster, time.Second, Options{})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	watch := takeNamespace(t, source.opened)
	if err := watch.apply(kube.Change{Type: "REPLACE"}); err != nil {
		t.Fatal(err)
	}
	proof.mu.Lock()
	proof.fail = true
	proof.mu.Unlock()
	if err := takeNamespace(t, done); !errors.Is(err, kube.ErrWatchSetContract) {
		t.Fatal("identity failure kept the worker active", err)
	}
	takeNamespace(t, watch.stopped)
}

func TestSeparateSandboxCountPreservesGatewayIdentity(t *testing.T) {
	cluster := ksuid.New().String()
	row := gateway(cluster)
	ns, err := gatewayworkload.SandboxNamespace(row.GetMetadata().GetId())
	if err != nil {
		t.Fatal(err)
	}
	api := &apiFixture{rows: []*pb.Gateway{row}}
	proof := &namespaceProof{sandbox: true, uids: map[string]string{ns: "first-uid"}, calls: map[string]int{}}
	source := &namespacePods{opened: make(chan openedNamespace, 4)}
	writes := make(chan countWrite, 32)
	c, err := New(source, proof, api, &writerFixture{state: api, write: func(r *control.SetObservedSandboxCountRequest) error {
		writes <- countWrite{Namespace: r.Namespace, ClusterId: r.ClusterId, Count: r.Count}
		return nil
	}}, cluster, time.Second, Options{SandboxEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() { cancel(); takeNamespace(t, done) })
	watch := takeNamespace(t, source.opened)
	if watch.namespace != ns {
		t.Fatal("count did not watch the allocated Sandbox namespace")
	}
	waitCount := func(want int32) {
		t.Helper()
		for {
			got := takeNamespace(t, writes)
			if got.Namespace != row.Namespace || got.ClusterId != cluster {
				t.Fatal("count lost the Gateway identity", got)
			}
			if got.Count == want {
				return
			}
		}
	}
	if err := watch.apply(kube.Change{Type: "REPLACE", Objects: []kube.Object{record("first", ns, "Running", "1")}}); err != nil {
		t.Fatal(err)
	}
	waitCount(1)
	if err := watch.apply(kube.Change{Type: "ADDED", Object: record("second", ns, "Pending", "2")}); err != nil {
		t.Fatal(err)
	}
	waitCount(2)
	proof.mu.Lock()
	proof.uids[ns] = "replacement-uid"
	proof.mu.Unlock()
	replacement := takeNamespace(t, source.opened)
	if replacement.namespace != ns {
		t.Fatal("wrong replacement namespace")
	}
	takeNamespace(t, watch.stopped)
	if err := replacement.apply(kube.Change{Type: "REPLACE"}); err != nil {
		t.Fatal(err)
	}
	waitCount(0)
	if err := watch.apply(kube.Change{Type: "ADDED", Object: record("late", ns, "Running", "3")}); err == nil {
		t.Fatal("old namespace accepted a count event")
	}
	proof.mu.Lock()
	defer proof.mu.Unlock()
	if proof.calls[ns] == 0 || proof.calls[row.Namespace] != 0 {
		t.Fatal("count checked the wrong allocation")
	}
}
