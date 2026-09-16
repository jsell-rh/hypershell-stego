package namespaceallocation

import (
	"context"
	"errors"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type allocations struct {
	calls []string
	done  bool
	err   error
}

func (a *allocations) Ensure(_ context.Context, p, n, id string) error {
	a.calls = append(a.calls, "ensure:"+p+":"+n+":"+id)
	return a.err
}
func (a *allocations) Delete(_ context.Context, p, n, id string) (bool, error) {
	a.calls = append(a.calls, "delete:"+p+":"+n+":"+id)
	return a.done, a.err
}

type stateAPI struct {
	control.GatewayIdentityServiceClient
	row *control.GetGatewayIdentityStateResponse
	err error
}

func (a *stateAPI) GetGatewayIdentityState(context.Context, *control.GetGatewayIdentityStateRequest, ...grpc.CallOption) (*control.GetGatewayIdentityStateResponse, error) {
	return a.row, a.err
}

func TestGatewayAllocationRequiresCurrentPlacement(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	ns, _ := gatewayworkload.Namespace(id)
	original := &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster}, ResourceVersion: 1, ResourceGeneration: 1, CleanupTargets: map[string]*control.CleanupTargetObservations{"workload": {Targets: map[string]bool{cluster: false}}, "sql": {Targets: map[string]bool{cluster: true}}}}
	for _, tc := range []struct {
		name, action string
		bad          bool
		edit         func(*stateAPI)
	}{
		{"current", "ensure", false, func(*stateAPI) {}},
		{"deleted", "delete", true, func(a *stateAPI) { a.row.Deleted = true }},
		{"moved", "delete", true, func(a *stateAPI) { a.row.Gateway.ClusterId = ksuid.New().String() }},
		{"unassigned", "", false, func(a *stateAPI) {
			a.row.Gateway.ClusterId = ksuid.New().String()
			delete(a.row.CleanupTargets["workload"].Targets, cluster)
		}},
		{"unrecorded", "", true, func(a *stateAPI) { delete(a.row.CleanupTargets["workload"].Targets, cluster) }},
		{"no history", "", true, func(a *stateAPI) { a.row.CleanupTargets = nil }},
		{"no version", "", true, func(a *stateAPI) { a.row.ResourceVersion = 0 }},
		{"wrong ID", "", true, func(a *stateAPI) { a.row.Gateway.Metadata.Id = ksuid.New().String() }},
		{"wrong namespace", "", true, func(a *stateAPI) { a.row.Gateway.Namespace = "foreign" }},
		{"missing", "", true, func(a *stateAPI) { a.row = nil; a.err = status.Error(codes.NotFound, "missing") }},
		{"denied", "", true, func(a *stateAPI) { a.err = status.Error(codes.PermissionDenied, "denied") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a := &stateAPI{row: proto.Clone(original).(*control.GetGatewayIdentityStateResponse)}
			tc.edit(a)
			writes := &allocations{done: true}
			c := &Controller{allocator: writes, state: a, cluster: cluster}
			err := c.reconcile(context.Background(), "gateway:"+id)
			if (err != nil) != tc.bad {
				t.Fatal(err)
			}
			if tc.action == "" {
				if len(writes.calls) != 0 {
					t.Fatal(writes.calls)
				}
			} else if tc.action == "ensure" {
				stateName, _ := gatewayworkload.StateNamespace(id)
				if len(writes.calls) != 2 || writes.calls[0] != "ensure:gateway-state:"+stateName+":"+id || writes.calls[1] != "ensure:gateway:"+ns+":"+id {
					t.Fatal(writes.calls)
				}
			} else if len(writes.calls) != 1 || writes.calls[0] != tc.action+":gateway:"+ns+":"+id {
				t.Fatal(writes.calls)
			}
		})
	}
}
func TestPendingCleanupAndProviderErrorsArePreserved(t *testing.T) {
	writes := &allocations{}
	c := &Controller{allocator: writes}
	if !errors.Is(c.remove(context.Background(), "gateway", "ns", "id"), ErrPending) {
		t.Fatal("pending cleanup was lost")
	}
	failure := errors.New("failed")
	writes.err = failure
	if !errors.Is(c.remove(context.Background(), "gateway", "ns", "id"), failure) {
		t.Fatal("cleanup failure was lost")
	}
}
func TestResourceKindsRemainDistinct(t *testing.T) {
	source := runtime.Source[string]{Watch: func(context.Context) (func() (string, error), error) {
		return func() (string, error) { return "same-id", nil }, nil
	}, Scan: func(_ context.Context, emit func(string) error) error { return emit("same-id") }}
	for _, prefix := range []string{"gateway:", "database:"} {
		s := tag(prefix, source)
		next, err := s.Watch(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		key, err := next()
		if err != nil || key != prefix+"same-id" {
			t.Fatal(key, err)
		}
		if err = s.Scan(context.Background(), func(key string) error {
			if key != prefix+"same-id" {
				t.Fatal(key)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestStateAllocationRemainsUntilSQLCleanupCompletes(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	ns, _ := gatewayworkload.Namespace(id)
	stateName, _ := gatewayworkload.StateNamespace(id)
	for _, sqlComplete := range []bool{false, true} {
		for _, workloadComplete := range []bool{false, true} {
			a := &stateAPI{row: &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster}, ResourceVersion: 1, ResourceGeneration: 1, Deleted: true, CleanupTargets: map[string]*control.CleanupTargetObservations{"workload": {Targets: map[string]bool{cluster: workloadComplete}}, "sql": {Targets: map[string]bool{cluster: sqlComplete}}}}}
			writes := &allocations{done: true}
			c := &Controller{allocator: writes, state: a, cluster: cluster}
			err := c.reconcile(context.Background(), "gateway:"+id)
			if sqlComplete && workloadComplete {
				if err != nil || len(writes.calls) != 3 || writes.calls[2] != "delete:gateway-state:"+stateName+":"+id || writes.calls[1] != "delete:gateway-console-state:openshell-console-"+stateName[len("openshell-state-"):]+":"+id {
					t.Fatal("completed state was not removed", err, writes.calls)
				}
			} else if !errors.Is(err, ErrPending) || len(writes.calls) != 1 || writes.calls[0] != "delete:gateway:"+ns+":"+id {
				t.Fatal("SQL state was removed early", sqlComplete, workloadComplete, err, writes.calls)
			}
		}
	}
}

func TestConsoleStateAllocationPrecedesWorkload(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	ns, _ := gatewayworkload.Namespace(id)
	state, _ := gatewayworkload.StateNamespace(id)
	consoleState, _ := gatewayworkload.ConsoleStateNamespace(id)
	address := "https://console.example.test"
	api := &stateAPI{row: &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster, ConsoleAddress: &address}, ResourceVersion: 1, ResourceGeneration: 1, CleanupTargets: map[string]*control.CleanupTargetObservations{"workload": {Targets: map[string]bool{cluster: false}}, "sql": {Targets: map[string]bool{cluster: false}}}}}
	writes := &allocations{done: true}
	controller := &Controller{allocator: writes, state: api, cluster: cluster}
	if err := controller.reconcile(context.Background(), "gateway:"+id); err != nil {
		t.Fatal(err)
	}
	expected := []string{"ensure:gateway-state:" + state + ":" + id, "ensure:gateway-console-state:" + consoleState + ":" + id, "ensure:gateway:" + ns + ":" + id}
	if len(writes.calls) != len(expected) {
		t.Fatal(writes.calls)
	}
	for i, value := range expected {
		if writes.calls[i] != value {
			t.Fatal(writes.calls)
		}
	}
}
