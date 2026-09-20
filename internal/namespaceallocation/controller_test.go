package namespaceallocation

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

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
	calls          []string
	done           bool
	err            error
	blockedProfile string
	deleteHook     func(context.Context)
}

func (a *allocations) Ensure(_ context.Context, p, n, id string) error {
	a.calls = append(a.calls, "ensure:"+p+":"+n+":"+id)
	return a.err
}
func (a *allocations) Delete(ctx context.Context, p, n, id string) (bool, error) {
	a.calls = append(a.calls, "delete:"+p+":"+n+":"+id)
	if a.deleteHook != nil {
		a.deleteHook(ctx)
	}
	return a.done && p != a.blockedProfile, a.err
}

type stateAPI struct {
	control.GatewayIdentityServiceClient
	row          *control.GetGatewayIdentityStateResponse
	err          error
	observations []*control.ObserveGatewayCleanupRequest
	versions     []string
	commitErr    error
	commitHook   func(context.Context)
}

func (a *stateAPI) GetGatewayIdentityState(context.Context, *control.GetGatewayIdentityStateRequest, ...grpc.CallOption) (*control.GetGatewayIdentityStateResponse, error) {
	return a.row, a.err
}

func (a *stateAPI) ObserveGatewayCleanup(ctx context.Context, request *control.ObserveGatewayCleanupRequest, _ ...grpc.CallOption) (*control.ObserveGatewayCleanupResponse, error) {
	a.observations = append(a.observations, proto.Clone(request).(*control.ObserveGatewayCleanupRequest))
	md, _ := metadata.FromOutgoingContext(ctx)
	a.versions = append(a.versions, md.Get("if-resource-version")...)
	if a.commitHook != nil {
		a.commitHook(ctx)
	}
	return &control.ObserveGatewayCleanupResponse{}, a.commitErr
}

func TestPendingCleanupAndProviderErrorsArePreserved(t *testing.T) {
	providerFailure := errors.New("provider failure")
	commitFailure := status.Error(codes.Aborted, "state conflict")
	for _, tc := range []struct {
		name            string
		done, prior     bool
		provider, write error
		pending         bool
		observations    int
	}{
		{name: "pending", pending: true},
		{name: "pending clears prior proof", prior: true, pending: true, observations: 1},
		{name: "provider failed", provider: providerFailure},
		{name: "provider failed after prior proof", prior: true, provider: providerFailure, observations: 1},
		{name: "provider complete with error", done: true, provider: providerFailure},
		{name: "pending write failed", prior: true, write: commitFailure, observations: 1},
		{name: "provider and write failed", prior: true, provider: providerFailure, write: commitFailure, observations: 1},
		{name: "complete", done: true, observations: 1},
		{name: "complete write failed", done: true, write: commitFailure, observations: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id, cluster := ksuid.New().String(), ksuid.New().String()
			ns, _ := gatewayworkload.Namespace(id)
			api := &stateAPI{row: &control.GetGatewayIdentityStateResponse{
				Gateway:         &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster},
				ResourceVersion: 37, ResourceGeneration: 9, Deleted: true,
				CleanupTargets: map[string]*control.CleanupTargetObservations{
					"workload":   {Targets: map[string]bool{cluster: true}},
					"sql":        {Targets: map[string]bool{cluster: true}},
					"allocation": {Targets: map[string]bool{cluster: tc.prior}},
				},
			}, commitErr: tc.write}
			writes := &allocations{done: tc.done, err: tc.provider}
			c := &Controller{allocator: writes, state: api, cluster: cluster}
			result, err := c.reconcile(context.Background(), "gateway:"+id)
			if (err != nil) != (tc.provider != nil || tc.write != nil) {
				t.Fatal("failure state changed", result, err)
			}
			for _, failure := range []error{tc.provider, tc.write} {
				if failure != nil && !errors.Is(err, failure) {
					t.Fatal("a provider or state-write error was lost", result, err)
				}
			}
			want := time.Duration(0)
			if tc.pending {
				want = cleanupRecheck
			}
			if result.RecheckAfter != want || len(api.observations) != tc.observations {
				t.Fatal("wrong recheck or observation count", result, len(api.observations))
			}
			if len(api.observations) != 0 {
				observation := api.observations[0]
				if observation.GetComplete() != (tc.done && tc.provider == nil) || observation.GetOwner() != "allocation" || observation.GetTarget() != cluster || !slices.Equal(api.versions, []string{"37"}) {
					t.Fatal("cleanup proof lost its result or read scope", observation, api.versions)
				}
			}
		})
	}
}

func TestAllocationPendingCancellationDoesNotBecomeProgress(t *testing.T) {
	for _, stage := range []string{"before work", "during work", "during commit", "expired deadline"} {
		t.Run(stage, func(t *testing.T) {
			id, cluster := ksuid.New().String(), ksuid.New().String()
			ns, _ := gatewayworkload.Namespace(id)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			wantFailure := error(context.Canceled)
			if stage == "expired deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithDeadline(ctx, time.Now().Add(-time.Second))
				defer stop()
				wantFailure = context.DeadlineExceeded
			}
			api := &stateAPI{row: &control.GetGatewayIdentityStateResponse{
				Gateway:         &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster},
				ResourceVersion: 37, ResourceGeneration: 9, Deleted: true,
				CleanupTargets: map[string]*control.CleanupTargetObservations{
					"workload":   {Targets: map[string]bool{cluster: true}},
					"sql":        {Targets: map[string]bool{cluster: true}},
					"allocation": {Targets: map[string]bool{cluster: true}},
				},
			}}
			writes := &allocations{}
			switch stage {
			case "before work":
				cancel()
			case "during work":
				writes.deleteHook = func(context.Context) { cancel() }
			case "during commit":
				api.commitHook = func(context.Context) { cancel() }
			}
			c := &Controller{allocator: writes, state: api, cluster: cluster}
			result, err := c.reconcile(ctx, "gateway:"+id)
			if !errors.Is(err, wantFailure) || result.RecheckAfter != 0 {
				t.Fatal("cancellation became expected progress", result, err)
			}
			wantWrites := 0
			if stage == "during commit" {
				wantWrites = 1
			}
			if len(api.observations) != wantWrites {
				t.Fatal("commit ran after cancellation", len(api.observations))
			}
		})
	}
}

func TestGatewayAllocationRequiresCurrentPlacement(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	ns, _ := gatewayworkload.Namespace(id)
	original := &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster}, ResourceVersion: 1, ResourceGeneration: 1, CleanupTargets: map[string]*control.CleanupTargetObservations{"allocation": {Targets: map[string]bool{cluster: false}}, "workload": {Targets: map[string]bool{cluster: false}}, "sql": {Targets: map[string]bool{cluster: true}}}}
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
			result, err := c.reconcile(context.Background(), "gateway:"+id)
			if (err != nil) != tc.bad {
				t.Fatal(err)
			}
			wantRecheck := time.Duration(0)
			if tc.action == "delete" {
				wantRecheck = cleanupRecheck
			}
			if result.RecheckAfter != wantRecheck {
				t.Fatal("wrong allocation recheck", result, err)
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
			} else if len(writes.calls) != 2 || writes.calls[0] != tc.action+":gateway:"+ns+":"+id || !strings.HasPrefix(writes.calls[1], "delete:sandbox:") {
				t.Fatal(writes.calls)
			}
		})
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
			a := &stateAPI{row: &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster}, ResourceVersion: 1, ResourceGeneration: 1, Deleted: true, CleanupTargets: map[string]*control.CleanupTargetObservations{"allocation": {Targets: map[string]bool{cluster: false}}, "workload": {Targets: map[string]bool{cluster: workloadComplete}}, "sql": {Targets: map[string]bool{cluster: sqlComplete}}}}}
			writes := &allocations{done: true}
			c := &Controller{allocator: writes, state: a, cluster: cluster}
			result, err := c.reconcile(context.Background(), "gateway:"+id)
			if sqlComplete && workloadComplete {
				if err != nil || result.RecheckAfter != 0 || len(writes.calls) != 4 || writes.calls[3] != "delete:gateway-state:"+stateName+":"+id || writes.calls[2] != "delete:gateway-console-state:openshell-console-"+stateName[len("openshell-state-"):]+":"+id {
					t.Fatal("completed state was not removed", err, writes.calls)
				}
			} else if err != nil || result.RecheckAfter != cleanupRecheck || len(writes.calls) != 2 || writes.calls[0] != "delete:gateway:"+ns+":"+id {
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
	for _, enabled := range []bool{false, true} {
		// Check initial provisioning, a ready endpoint, and a withdrawn endpoint.
		for _, address := range []string{"", "https://console.example.test", ""} {
			api := &stateAPI{row: &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster, ConsoleAddress: &address}, ResourceVersion: 1, ResourceGeneration: 1, CleanupTargets: map[string]*control.CleanupTargetObservations{"allocation": {Targets: map[string]bool{cluster: false}}, "workload": {Targets: map[string]bool{cluster: false}}, "sql": {Targets: map[string]bool{cluster: false}}}}}
			writes := &allocations{done: true}
			controller := &Controller{allocator: writes, state: api, cluster: cluster, console: enabled}
			if result, err := controller.reconcile(context.Background(), "gateway:"+id); err != nil || result.RecheckAfter != 0 {
				t.Fatal(err)
			}
			expected := []string{"ensure:gateway-state:" + state + ":" + id}
			if enabled {
				expected = append(expected, "ensure:gateway-console-state:"+consoleState+":"+id)
			}
			expected = append(expected, "ensure:gateway:"+ns+":"+id)
			if !slices.Equal(writes.calls, expected) {
				t.Fatalf("allocation depended on readiness: enabled=%t address=%q calls=%v", enabled, address, writes.calls)
			}
		}
	}
}

func TestConsoleAllocationConfiguration(t *testing.T) {
	cluster := ksuid.New().String()
	for _, domain := range []string{"", "console.example.test", "https://console.example.test", "console.example.test/path"} {
		controller, err := New(cluster, &allocations{}, pb.NewGatewayServiceClient(nil), &stateAPI{}, Options{ConsoleDomain: domain})
		valid := domain == "" || domain == "console.example.test"
		if valid {
			if err != nil || controller.console != (domain != "") {
				t.Fatalf("valid console configuration failed: %q: %v", domain, err)
			}
		} else if err == nil {
			t.Fatalf("invalid console domain accepted: %q", domain)
		}
	}
}

func TestSandboxAllocationFollowsGatewayIdentityAndSurvivesOptionChange(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	ns, _ := gatewayworkload.Namespace(id)
	sandbox, _ := gatewayworkload.SandboxNamespace(id)
	api := &stateAPI{row: &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster}, ResourceVersion: 1, ResourceGeneration: 1, CleanupTargets: map[string]*control.CleanupTargetObservations{"allocation": {Targets: map[string]bool{cluster: false}}, "workload": {Targets: map[string]bool{cluster: true}}, "sql": {Targets: map[string]bool{cluster: true}}}}}
	writes := &allocations{done: true}
	controller := &Controller{allocator: writes, state: api, cluster: cluster, sandbox: true}
	if result, err := controller.reconcile(context.Background(), "gateway:"+id); err != nil || result.RecheckAfter != 0 {
		t.Fatal(err)
	}
	if len(writes.calls) != 3 || writes.calls[1] != "ensure:gateway:"+ns+":"+id || writes.calls[2] != "ensure:sandbox:"+sandbox+":"+id {
		t.Fatal("Sandbox did not follow Gateway allocation", writes.calls)
	}
	// A new process can disable creation after an earlier process allocated it.
	restarted := &Controller{allocator: writes, state: api, cluster: cluster}
	api.row.Deleted = true
	writes.calls = nil
	if result, err := restarted.reconcile(context.Background(), "gateway:"+id); err != nil || result.RecheckAfter != 0 {
		t.Fatal(err)
	}
	if len(writes.calls) != 4 || writes.calls[0] != "delete:gateway:"+ns+":"+id || writes.calls[1] != "delete:sandbox:"+sandbox+":"+id {
		t.Fatal("disabled option orphaned Sandbox allocation", writes.calls)
	}
}

func TestAllocationCleanupCommitRequiresAllNamespacesAbsent(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	ns, _ := gatewayworkload.Namespace(id)
	for _, tc := range []struct {
		name, blocked    string
		prior            bool
		commitCode       codes.Code
		wantCalls        int
		wantObservations int
	}{
		{name: "complete", wantCalls: 4, wantObservations: 1},
		{name: "retained console", blocked: "gateway-console-state", wantCalls: 4},
		{name: "retained state", blocked: "gateway-state", wantCalls: 4},
		{name: "repeat complete", prior: true, wantCalls: 4},
		{name: "absence lost", prior: true, blocked: "gateway-state", wantCalls: 4, wantObservations: 1},
		{name: "denied", commitCode: codes.PermissionDenied, wantCalls: 4, wantObservations: 1},
		{name: "stale version", commitCode: codes.Aborted, wantCalls: 4, wantObservations: 1},
		{name: "pending denied", prior: true, blocked: "gateway-state", commitCode: codes.PermissionDenied, wantCalls: 4, wantObservations: 1},
		{name: "pending stale version", prior: true, blocked: "gateway-state", commitCode: codes.Aborted, wantCalls: 4, wantObservations: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &stateAPI{row: &control.GetGatewayIdentityStateResponse{
				Gateway:         &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: ns, ClusterId: cluster},
				ResourceVersion: 37, ResourceGeneration: 9, Deleted: true,
				CleanupTargets: map[string]*control.CleanupTargetObservations{
					"workload":   {Targets: map[string]bool{cluster: true}},
					"sql":        {Targets: map[string]bool{cluster: true}},
					"allocation": {Targets: map[string]bool{cluster: tc.prior}},
				},
			}, commitErr: status.Error(tc.commitCode, "commit failure")}
			writes := &allocations{done: true, blockedProfile: tc.blocked}
			controller := &Controller{allocator: writes, state: api, cluster: cluster}
			result, err := controller.reconcile(context.Background(), "gateway:"+id)
			if tc.blocked != "" && tc.commitCode == codes.OK {
				if err != nil || result.RecheckAfter != cleanupRecheck {
					t.Fatal("pending result was lost", err)
				}
			} else if status.Code(err) != tc.commitCode || result.RecheckAfter != 0 {
				t.Fatal("commit result was lost", err)
			}
			if len(writes.calls) != tc.wantCalls || len(api.observations) != tc.wantObservations {
				t.Fatal("wrong cleanup or commit count", writes.calls, len(api.observations))
			}
			if tc.wantObservations == 1 {
				observation := api.observations[0]
				if observation.GetId() != id || observation.GetOwner() != "allocation" || observation.GetTarget() != cluster || observation.GetComplete() != (tc.blocked == "") || !slices.Equal(api.versions, []string{"37"}) {
					t.Fatal("allocation observation changed its read scope", observation, api.versions)
				}
			}
		})
	}
}
