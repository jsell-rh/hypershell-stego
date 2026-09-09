package sandboxcount

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func record(uid, ns, phase, version string) kube.Object {
	return kube.Object{"metadata": map[string]any{"uid": uid, "namespace": ns, "resourceVersion": version, "labels": map[string]any{SandboxLabel: "sandbox"}}, "status": map[string]any{"phase": phase}}
}
func TestActiveTransitionsDuplicateDeliveryAndReset(t *testing.T) {
	ns := "openshell-0123456789abcdef"
	separate := "openshell-sandbox-0123456789abcdef"
	o := newObservation()
	apply := func(kind string, object kube.Object, want int32) {
		t.Helper()
		if err := o.consume(kube.Change{Type: kind, Object: object}); err != nil {
			t.Fatal(err)
		}
		if o.counts[ns] != want {
			t.Fatal(kind, o.counts, want)
		}
	}
	if err := o.consume(kube.Change{Type: "REPLACE", Objects: []kube.Object{record("one", ns, "Pending", "opaque-a"), record("two", separate, "Running", "opaque-b")}}); err != nil {
		t.Fatal(err)
	}
	o.changed = map[string]bool{}
	apply("ADDED", record("one", ns, "Pending", "opaque-a"), 2)
	apply("MODIFIED", record("one", ns, "Running", "opaque-c"), 2)
	if len(o.changed) != 0 {
		t.Fatal("active transition changed count")
	}
	apply("MODIFIED", record("one", ns, "Failed", "opaque-d"), 1)
	apply("DELETED", record("one", ns, "Failed", "opaque-d"), 1)
	apply("DELETED", record("one", ns, "Failed", "opaque-d"), 1)
	apply("ADDED", record("replacement", ns, "Pending", "opaque-e"), 2)
	apply("DELETED", record("two", separate, "Running", "opaque-f"), 1)
	unlabeled := record("ignored", ns, "Running", "a")
	delete(unlabeled["metadata"].(map[string]any), "labels")
	apply("ADDED", unlabeled, 1)
	apply("ADDED", record("foreign", "other-namespace", "Running", "a"), 1)
	if err := o.consume(kube.Change{Type: "RESET"}); err != nil || o.ready {
		t.Fatal(err)
	}
	if err := o.consume(kube.Change{Type: "MODIFIED", Object: record("replacement", ns, "Failed", "b")}); err == nil {
		t.Fatal("used partial baseline")
	}
	if err := o.consume(kube.Change{Type: "REPLACE", Objects: []kube.Object{}}); err != nil || !o.ready || o.counts[ns] != 0 || !o.changed[ns] {
		t.Fatal("reset did not remove missing Pods", err)
	}
}

type sourceFixture struct{ ready chan func(kube.Change) error }

func (s *sourceFixture) Observe(ctx context.Context, c kube.Collection, apply func(kube.Change) error) error {
	if c.Path != "/api/v1/pods" || c.LabelSelector != SandboxLabel {
		return errors.New("invalid Pod watch")
	}
	s.ready <- apply
	<-ctx.Done()
	return ctx.Err()
}

type apiFixture struct {
	pb.GatewayServiceClient
	rows []*pb.Gateway
}

func (a *apiFixture) ListGateways(_ context.Context, r *pb.ListGatewaysRequest, _ ...grpc.CallOption) (*pb.ListGatewaysResponse, error) {
	if r.Page != 1 || r.Size != 100 {
		return nil, errors.New("unbounded catalog request")
	}
	return &pb.ListGatewaysResponse{Items: a.rows}, nil
}

type writerFixture struct {
	control.GatewayIdentityServiceClient
	write func(*control.SetObservedSandboxCountRequest) error
}

func (w *writerFixture) SetObservedSandboxCount(_ context.Context, r *control.SetObservedSandboxCountRequest, _ ...grpc.CallOption) (*control.SetObservedSandboxCountResponse, error) {
	err := w.write(r)
	return &control.SetObservedSandboxCountResponse{Count: r.Count}, err
}
func take(t *testing.T, ch <-chan int32) int32 {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(3 * time.Second):
		t.Fatal("count write did not arrive")
		return 0
	}
}
func TestCountWritesSerializeAndRecoverFromCache(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	ns, _ := gatewayworkload.Namespace(id)
	source := &sourceFixture{make(chan func(kube.Change) error, 1)}
	api := &apiFixture{rows: []*pb.Gateway{{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster}}}
	calls := make(chan int32, 20)
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var mu sync.Mutex
	stored := int32(7)
	number := 0
	writer := &writerFixture{write: func(r *control.SetObservedSandboxCountRequest) error {
		if r.Namespace != ns || r.ClusterId != cluster {
			t.Error("wrong count assignment", r)
		}
		number++
		calls <- r.Count
		if number == 1 {
			<-release
		}
		if number == 3 {
			return status.Error(codes.Unavailable, "retry")
		}
		mu.Lock()
		stored = r.Count
		mu.Unlock()
		return nil
	}}
	c, err := New(source, api, writer, cluster, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("controller did not stop")
		}
	})
	apply := <-source.ready
	if err := apply(kube.Change{Type: "REPLACE", Objects: []kube.Object{record("one", ns, "Running", "a")}}); err != nil {
		t.Fatal(err)
	}
	if v := take(t, calls); v != 1 {
		t.Fatal("baseline was added to stored count", v)
	}
	if err := apply(kube.Change{Type: "ADDED", Object: record("two", ns, "Pending", "b")}); err != nil {
		t.Fatal(err)
	}
	unblock()
	if v := take(t, calls); v != 2 {
		t.Fatal("new observation was lost", v)
	}
	if err := apply(kube.Change{Type: "DELETED", Object: record("one", ns, "Running", "c")}); err != nil {
		t.Fatal(err)
	}
	if v := take(t, calls); v != 1 {
		t.Fatal(v)
	}
	if err := apply(kube.Change{Type: "DELETED", Object: record("two", ns, "Pending", "d")}); err != nil {
		t.Fatal(err)
	}
	if v := take(t, calls); v != 0 {
		t.Fatal("retry used stale delta", v)
	}
	waitZero := time.Now().Add(time.Second)
	for {
		mu.Lock()
		v := stored
		mu.Unlock()
		if v == 0 {
			break
		}
		if time.Now().After(waitZero) {
			t.Fatal("zero was not stored")
		}
		time.Sleep(time.Millisecond)
	}
	mu.Lock()
	stored = 9
	mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		value := stored
		mu.Unlock()
		if value == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("cache did not heal drift", value)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func TestCountAccessLossStopsWatch(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	ns, _ := gatewayworkload.Namespace(id)
	source := &sourceFixture{make(chan func(kube.Change) error, 1)}
	c, err := New(source, &apiFixture{rows: []*pb.Gateway{{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster}}}, &writerFixture{write: func(*control.SetObservedSandboxCountRequest) error {
		return status.Error(codes.PermissionDenied, "denied")
	}}, cluster, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() { done <- c.Run(ctx) }()
	apply := <-source.ready
	if err := apply(kube.Change{Type: "REPLACE"}); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if status.Code(err) != codes.PermissionDenied {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("access loss did not stop the watch")
	}
}

func TestCountWaitsForReplacementAfterReset(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	ns, _ := gatewayworkload.Namespace(id)
	source := &sourceFixture{make(chan func(kube.Change) error, 1)}
	calls := make(chan int32, 10)
	release := make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	defer unblock()
	number := 0
	writer := &writerFixture{write: func(r *control.SetObservedSandboxCountRequest) error {
		number++
		calls <- r.Count
		if number == 1 {
			<-release
		}
		return nil
	}}
	c, err := New(source, &apiFixture{rows: []*pb.Gateway{{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster}}}, writer, cluster, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	t.Cleanup(func() {
		unblock()
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("count controller did not stop")
		}
	})
	apply := <-source.ready
	if err := apply(kube.Change{Type: "REPLACE", Objects: []kube.Object{record("one", ns, "Running", "a")}}); err != nil {
		t.Fatal(err)
	}
	if take(t, calls) != 1 {
		t.Fatal("wrong initial count")
	}
	// The existing write may finish, but no later write may use an incomplete cache.
	if err := apply(kube.Change{Type: "RESET"}); err != nil {
		t.Fatal(err)
	}
	unblock()
	select {
	case got := <-calls:
		t.Fatalf("wrote %d before a complete replacement", got)
	case <-time.After(1100 * time.Millisecond):
	}
	if err := apply(kube.Change{Type: "REPLACE"}); err != nil {
		t.Fatal(err)
	}
	if take(t, calls) != 0 {
		t.Fatal("replacement did not clear the missing Pod")
	}
}
