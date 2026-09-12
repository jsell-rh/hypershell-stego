package namespaceallocation

import (
	"context"
	"errors"
	"strconv"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
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

type databaseAPI struct {
	pb.ManagedDatabaseServiceClient
	row      *pb.ManagedDatabase
	header   metadata.MD
	err      error
	retained bool
}

func (a *databaseAPI) GetManagedDatabase(ctx context.Context, _ *pb.GetManagedDatabaseRequest, options ...grpc.CallOption) (*pb.GetManagedDatabaseResponse, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	a.retained = len(md.Get("resource-read-mode")) == 1 && md.Get("resource-read-mode")[0] == "retained-v1"
	for _, o := range options {
		if h, ok := o.(grpc.HeaderCallOption); ok {
			*h.HeaderAddr = a.header
		}
	}
	return &pb.GetManagedDatabaseResponse{ManagedDatabase: a.row}, a.err
}
func TestGatewayAllocationRequiresCurrentPlacement(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	ns, _ := gatewayworkload.Namespace(id)
	original := &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster}, ResourceVersion: 1, ResourceGeneration: 1, CleanupTargets: map[string]*control.CleanupTargetObservations{"workload": {Targets: map[string]bool{cluster: false}}}}
	for _, tc := range []struct {
		name, action string
		bad          bool
		edit         func(*stateAPI)
	}{
		{"current", "ensure", false, func(*stateAPI) {}},
		{"deleted", "delete", false, func(a *stateAPI) { a.row.Deleted = true }},
		{"moved", "delete", false, func(a *stateAPI) { a.row.Gateway.ClusterId = ksuid.New().String() }},
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
			err := c.reconcile(context.Background(), "gateway:"+id, nil)
			if (err != nil) != tc.bad {
				t.Fatal(err)
			}
			if tc.action == "" {
				if len(writes.calls) != 0 {
					t.Fatal(writes.calls)
				}
			} else if len(writes.calls) != 1 || writes.calls[0] != tc.action+":gateway:"+ns+":"+id {
				t.Fatal(writes.calls)
			}
		})
	}
}
func TestDatabaseAllocationRequiresRetainedStateAndPlacement(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	ns, _ := gateways.DatabaseNamespace(id)
	for _, tc := range []struct {
		name                           string
		deleted, denied, missingHeader bool
		provider                       string
		calls                          int
	}{
		{name: "live", provider: "deployment", calls: 1}, {name: "deleted", provider: "deployment", deleted: true, calls: 1}, {name: "wrong cluster", provider: "deployment", denied: true}, {name: "missing state", provider: "deployment", missingHeader: true}, {name: "shared CNPG", provider: "cnpg"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &databaseAPI{row: &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, Provider: tc.provider}, header: metadata.Pairs("resource-version", "2", "resource-deleted", strconv.FormatBool(tc.deleted))}
			if tc.missingHeader {
				api.header = nil
			}
			writes := &allocations{done: true}
			c := &Controller{allocator: writes, databases: api, cluster: cluster}
			checks := 0
			err := c.reconcile(context.Background(), "database:"+id, func(_ context.Context, db *pb.ManagedDatabase, target string) error {
				checks++
				if db != api.row || target != cluster {
					t.Fatal("placement input changed")
				}
				if tc.denied {
					return errors.New("ambiguous placement")
				}
				return nil
			})
			if (err != nil) != (tc.denied || tc.missingHeader) {
				t.Fatal(err)
			}
			if !api.retained || len(writes.calls) != tc.calls {
				t.Fatal(api.retained, writes.calls)
			}
			if tc.provider == "cnpg" && checks != 0 {
				t.Fatal("CNPG entered deployment allocation")
			}
			if tc.deleted && len(writes.calls) == 1 && writes.calls[0] != "delete:database:"+ns+":"+id {
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
	if err := c.Run(context.Background(), nil, nil); err == nil {
		t.Fatal("missing placement accepted")
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
