package namespaceallocation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type stateRemovalResult struct {
	done bool
	err  error
}
type stateRemovals struct {
	allocations
	results map[string]stateRemovalResult
}

func (a *stateRemovals) Delete(ctx context.Context, profile, name, id string) (bool, error) {
	done, err := a.allocations.Delete(ctx, profile, name, id)
	if result, ok := a.results[profile]; ok {
		return result.done, result.err
	}
	return done, err
}

func stateCleanupFixture(t *testing.T) (*stateAPI, string, string) {
	t.Helper()
	id, cluster := ksuid.New().String(), ksuid.New().String()
	name, err := gatewayworkload.Namespace(id)
	if err != nil {
		t.Fatal(err)
	}
	return &stateAPI{row: &control.GetGatewayIdentityStateResponse{
		Gateway:         &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Namespace: name, ClusterId: cluster},
		ResourceVersion: 37, ResourceGeneration: 9, Deleted: true,
		CleanupTargets: map[string]*control.CleanupTargetObservations{
			"workload":   {Targets: map[string]bool{cluster: true}},
			"sql":        {Targets: map[string]bool{cluster: true}},
			"allocation": {Targets: map[string]bool{cluster: false}},
		},
	}}, id, cluster
}

func TestStateCleanupRequestsBothNamespacesBeforeWaiting(t *testing.T) {
	denied := status.Error(codes.PermissionDenied, "state deletion denied")
	failed := errors.New("state deletion failed")
	for _, tc := range []struct {
		name           string
		console, state stateRemovalResult
		calls          int
		failure        error
		complete       bool
	}{
		{name: "console pending", state: stateRemovalResult{done: true}, calls: 4},
		{name: "state pending", console: stateRemovalResult{done: true}, calls: 4},
		{name: "both pending", calls: 4},
		{name: "both absent", console: stateRemovalResult{done: true}, state: stateRemovalResult{done: true}, calls: 4, complete: true},
		{name: "console denied", console: stateRemovalResult{err: denied}, calls: 3, failure: denied},
		{name: "state failed", console: stateRemovalResult{done: true}, state: stateRemovalResult{err: failed}, calls: 4, failure: failed},
		{name: "console pending and state failed", state: stateRemovalResult{err: failed}, calls: 4, failure: failed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, id, cluster := stateCleanupFixture(t)
			writes := &stateRemovals{allocations: allocations{done: true}, results: map[string]stateRemovalResult{
				"gateway-console-state": tc.console, "gateway-state": tc.state,
			}}
			c := &Controller{allocator: writes, state: api, cluster: cluster}
			result, err := c.reconcile(context.Background(), "gateway:"+id)
			if !errors.Is(err, tc.failure) || (err == nil) != (tc.failure == nil) {
				t.Fatal("deletion error changed", err)
			}
			if len(writes.calls) != tc.calls {
				t.Fatal("an independent state deletion was delayed", writes.calls)
			}
			if tc.failure != nil || tc.complete {
				if result.RecheckAfter != 0 {
					t.Fatal("failure or completion became pending", result)
				}
			} else if result.RecheckAfter != cleanupRecheck {
				t.Fatal("pending deletion lost its schedule", result)
			}
			if tc.complete {
				if len(api.observations) != 1 || !api.observations[0].GetComplete() {
					t.Fatal("complete deletion was not recorded", api.observations)
				}
			} else if len(api.observations) != 0 {
				t.Fatal("partial deletion was recorded as complete", api.observations)
			}
		})
	}
}

func TestStateCleanupStopsRequestsAfterCancellation(t *testing.T) {
	api, id, cluster := stateCleanupFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	writes := &stateRemovals{allocations: allocations{done: true}, results: map[string]stateRemovalResult{
		"gateway-console-state": {},
	}}
	writes.deleteHook = func(context.Context) {
		if strings.HasPrefix(writes.calls[len(writes.calls)-1], "delete:gateway-console-state:") {
			cancel()
		}
	}
	c := &Controller{allocator: writes, state: api, cluster: cluster}
	result, err := c.reconcile(ctx, "gateway:"+id)
	if !errors.Is(err, context.Canceled) || result.RecheckAfter != 0 || len(writes.calls) != 3 || len(api.observations) != 0 {
		t.Fatal("cancellation started another delete or recorded progress", result, err, writes.calls, api.observations)
	}
}

func TestStateCleanupResumesAfterControllerRestart(t *testing.T) {
	api, id, cluster := stateCleanupFixture(t)
	writes := &stateRemovals{allocations: allocations{done: true}, results: map[string]stateRemovalResult{
		"gateway-console-state": {}, "gateway-state": {},
	}}
	for step := 0; step < 3; step++ {
		// The API record survives. Each step uses a new controller instance.
		c := &Controller{allocator: writes, state: api, cluster: cluster}
		writes.calls = nil
		if step == 1 {
			writes.results["gateway-console-state"] = stateRemovalResult{done: true}
		}
		if step == 2 {
			writes.results["gateway-state"] = stateRemovalResult{done: true}
		}
		result, err := c.reconcile(context.Background(), "gateway:"+id)
		if err != nil || len(writes.calls) != 4 {
			t.Fatal("restart lost retained deletion", step, result, err, writes.calls)
		}
		if step < 2 {
			if result.RecheckAfter != cleanupRecheck || len(api.observations) != 0 {
				t.Fatal("restart finalized partial deletion", step, result, api.observations)
			}
		} else if result.RecheckAfter != 0 || len(api.observations) != 1 || !api.observations[0].GetComplete() {
			t.Fatal("restart failed to complete deletion", result, api.observations)
		}
	}
}
