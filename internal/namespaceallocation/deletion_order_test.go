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

// The Gateway namespace deletion no longer gates the Sandbox namespace
// deletion request. Both requests happen in the same reconcile pass. A
// namespace with a deletion timestamp rejects new objects, so a Gateway that
// is still terminating cannot orphan Sandbox content. The state namespaces
// stay gated on recorded workload and SQL cleanup completion.

func TestCleanupRequestsSandboxDeletionWithGatewayDeletion(t *testing.T) {
	for _, tc := range []struct {
		name             string
		gateway, sandbox removal
		wantCalls        int
		wantObservations int
		pending          bool
	}{
		{name: "both absent", gateway: removal{done: true}, sandbox: removal{done: true}, wantCalls: 4, wantObservations: 1},
		{name: "gateway terminating", sandbox: removal{done: true}, wantCalls: 2, pending: true},
		{name: "sandbox terminating", gateway: removal{done: true}, wantCalls: 2, pending: true},
		{name: "both terminating", wantCalls: 2, pending: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, id, cluster := deletionFixture(t)
			writes := &profileRemovals{allocations: allocations{done: true}, results: map[string]removal{
				"gateway": tc.gateway, "sandbox": tc.sandbox,
				"gateway-state": {done: true}, "gateway-console-state": {done: true},
			}}
			c := &Controller{allocator: writes, state: api, cluster: cluster}
			result, err := c.reconcile(context.Background(), "gateway:"+id)
			if err != nil {
				t.Fatal("deletion became an error", err)
			}
			sandboxName, _ := gatewayworkload.SandboxNamespace(id)
			ns, _ := gatewayworkload.Namespace(id)
			if len(writes.calls) != tc.wantCalls ||
				writes.calls[0] != "delete:gateway:"+ns+":"+id ||
				writes.calls[1] != "delete:sandbox:"+sandboxName+":"+id {
				t.Fatal("the Sandbox deletion request was delayed", writes.calls)
			}
			wantRecheck := cleanupRecheck
			if !tc.pending {
				wantRecheck = 0
			}
			if result.RecheckAfter != wantRecheck {
				t.Fatal("pending schedule changed", result)
			}
			if len(api.observations) != tc.wantObservations {
				t.Fatal("completion record changed", api.observations)
			}
		})
	}
}

func TestCleanupSandboxFailureDoesNotSkipGatewayDeletion(t *testing.T) {
	api, id, cluster := deletionFixture(t)
	denied := status.Error(codes.PermissionDenied, "sandbox deletion denied")
	writes := &profileRemovals{allocations: allocations{done: true}, results: map[string]removal{
		"sandbox": {err: denied}, "gateway-state": {done: true}, "gateway-console-state": {done: true},
	}}
	c := &Controller{allocator: writes, state: api, cluster: cluster}
	result, err := c.reconcile(context.Background(), "gateway:"+id)
	if !errors.Is(err, denied) {
		t.Fatal("sandbox failure was lost", err)
	}
	if result.RecheckAfter != 0 {
		t.Fatal("failure became pending", result)
	}
	ns, _ := gatewayworkload.Namespace(id)
	if len(writes.calls) == 0 || writes.calls[0] != "delete:gateway:"+ns+":"+id {
		t.Fatal("gateway deletion was skipped after a sandbox failure", writes.calls)
	}
	if len(api.observations) != 0 {
		t.Fatal("failed pass recorded completion", api.observations)
	}
}

func TestCleanupGatewayFailureStillRequestsSandboxDeletion(t *testing.T) {
	api, id, cluster := deletionFixture(t)
	failed := errors.New("gateway deletion failed")
	writes := &profileRemovals{allocations: allocations{done: true}, results: map[string]removal{
		"gateway": {err: failed}, "gateway-state": {done: true}, "gateway-console-state": {done: true},
	}}
	c := &Controller{allocator: writes, state: api, cluster: cluster}
	if _, err := c.reconcile(context.Background(), "gateway:"+id); !errors.Is(err, failed) {
		t.Fatal("gateway failure was lost", err)
	}
	sandboxName, _ := gatewayworkload.SandboxNamespace(id)
	requested := false
	for _, call := range writes.calls {
		if call == "delete:sandbox:"+sandboxName+":"+id {
			requested = true
		}
	}
	if !requested {
		t.Fatal("a gateway failure stopped the sandbox deletion request", writes.calls)
	}
	if len(api.observations) != 0 {
		t.Fatal("failed pass recorded completion", api.observations)
	}
}

func TestCleanupStateNamespacesStillRequireCompletedCleanup(t *testing.T) {
	for _, tc := range []struct {
		name              string
		workload, sql     bool
		wantStateRequests bool
	}{
		{name: "workload incomplete", workload: false, sql: true},
		{name: "sql incomplete", workload: true, sql: false},
		{name: "both complete", workload: true, sql: true, wantStateRequests: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api, id, cluster := deletionFixture(t)
			api.row.CleanupTargets["workload"] = &control.CleanupTargetObservations{Targets: map[string]bool{cluster: tc.workload}}
			api.row.CleanupTargets["sql"] = &control.CleanupTargetObservations{Targets: map[string]bool{cluster: tc.sql}}
			writes := &profileRemovals{allocations: allocations{done: true}, results: map[string]removal{
				"gateway": {done: true}, "sandbox": {done: true},
				"gateway-state": {done: true}, "gateway-console-state": {done: true},
			}}
			c := &Controller{allocator: writes, state: api, cluster: cluster}
			result, err := c.reconcile(context.Background(), "gateway:"+id)
			if err != nil {
				t.Fatal(err)
			}
			stateRequested := false
			for _, call := range writes.calls {
				if strings.HasPrefix(call, "delete:gateway-state:") || strings.HasPrefix(call, "delete:gateway-console-state:") {
					stateRequested = true
				}
			}
			if stateRequested != tc.wantStateRequests {
				t.Fatal("state namespace gating changed", writes.calls)
			}
			wantRecheck := cleanupRecheck
			if tc.wantStateRequests {
				wantRecheck = 0
			}
			if result.RecheckAfter != wantRecheck {
				t.Fatal("pending schedule changed", result)
			}
		})
	}
}

type removal struct {
	done bool
	err  error
}

type profileRemovals struct {
	allocations
	results map[string]removal
}

func (a *profileRemovals) Delete(ctx context.Context, profile, name, id string) (bool, error) {
	done, err := a.allocations.Delete(ctx, profile, name, id)
	if result, ok := a.results[profile]; ok {
		return result.done, result.err
	}
	return done, err
}

func deletionFixture(t *testing.T) (*stateAPI, string, string) {
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
