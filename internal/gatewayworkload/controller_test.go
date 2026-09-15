package gatewayworkload

import (
	"context"
	"errors"
	"github.com/segmentio/ksuid"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type stateFixture struct {
	control.GatewayIdentityServiceClient
	state        *control.GetGatewayIdentityStateResponse
	err          error
	observations int
	conflict     bool
}

func (f *stateFixture) GetGatewayIdentityState(context.Context, *control.GetGatewayIdentityStateRequest, ...grpc.CallOption) (*control.GetGatewayIdentityStateResponse, error) {
	return f.state, f.err
}

type apiFixture struct {
	pb.GatewayServiceClient
	updates  int
	desired  string
	phase    string
	err      error
	endpoint *string
	version  string
}

func (f *apiFixture) UpdateGateway(ctx context.Context, r *pb.UpdateGatewayRequest, _ ...grpc.CallOption) (*pb.UpdateGatewayResponse, error) {
	f.updates++
	f.desired = r.GetStatus()
	f.phase = r.GetPhase()
	f.endpoint = r.RouteAddress
	md, _ := metadata.FromOutgoingContext(ctx)
	f.version = strings.Join(md.Get("if-resource-version"), ",")
	return &pb.UpdateGatewayResponse{}, f.err
}

type releaseFixture struct {
	pb.GatewayReleaseServiceClient
	row *pb.GatewayRelease
}

func (f *releaseFixture) GetGatewayRelease(context.Context, *pb.GetGatewayReleaseRequest, ...grpc.CallOption) (*pb.GetGatewayReleaseResponse, error) {
	return &pb.GetGatewayReleaseResponse{GatewayRelease: f.row}, nil
}

type providerFixture struct {
	target           string
	unassigned       bool
	creates, deletes int
	sqlDeletes       int
	err              error
	endpoint         *string
}

func (f *providerFixture) DesiredEndpoint(*pb.Gateway) *string { return f.endpoint }

func workloadHistory(target string) map[string]*control.CleanupTargetObservations {
	return map[string]*control.CleanupTargetObservations{"workload": {Targets: map[string]bool{target: false}}, "sql": {Targets: map[string]bool{target: true}}}
}
func (f *providerFixture) CleanupTarget() string {
	if f.target != "" {
		return f.target
	}
	return testClusterID
}
func (f *stateFixture) ObserveGatewayCleanup(ctx context.Context, r *control.ObserveGatewayCleanupRequest, _ ...grpc.CallOption) (*control.ObserveGatewayCleanupResponse, error) {
	f.observations++
	md, _ := metadata.FromOutgoingContext(ctx)
	versions := md.Get("if-resource-version")
	if len(versions) != 1 || versions[0] != strconv.FormatInt(f.state.ResourceVersion, 10) || (r.Owner != "workload" && r.Owner != "sql") || r.Target != testClusterID || r.Id != f.state.Gateway.Metadata.Id {
		return nil, status.Error(codes.InvalidArgument, "invalid target observation")
	}
	if f.conflict {
		return nil, status.Error(codes.Aborted, "resource changed")
	}
	f.state.CleanupTargets[r.Owner].Targets[r.Target] = r.Complete
	f.state.ResourceVersion++
	return &control.ObserveGatewayCleanupResponse{}, nil
}
func (f *providerFixture) Handles(*pb.Gateway) bool { return !f.unassigned }

func (f *providerFixture) Ensure(context.Context, *pb.Gateway, *pb.GatewayRelease) error {
	f.creates++
	return f.err
}
func (f *providerFixture) Delete(context.Context, *pb.Gateway) error {
	f.deletes++
	return f.err
}
func (f *providerFixture) DeleteDatabase(context.Context, *pb.Gateway) error {
	f.sqlDeletes++
	return f.err
}
func (f *providerFixture) GatewayIDs(context.Context) ([]string, error) { return nil, nil }

func TestDeletionRequiresExplicitCurrentState(t *testing.T) {
	for _, tc := range []struct {
		name  string
		state *control.GetGatewayIdentityStateResponse
		err   error
	}{
		{name: "missing revision", state: &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}}, Deleted: true}},
		{name: "denied", err: status.Error(codes.PermissionDenied, "denied")},
		{name: "absent", err: status.Error(codes.NotFound, "absent")},
		{name: "empty", state: &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), ResourceVersion: 1}},
		{name: "mismatch", state: &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), ResourceVersion: 1, ResourceGeneration: 1, ObservedGeneration: 1, Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "other"}}, Deleted: true}},
		{name: "deleted", state: &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), ResourceVersion: 1, ResourceGeneration: 1, ObservedGeneration: 1, Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}}, Deleted: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := new(providerFixture)
			api := new(apiFixture)
			c, err := New(api, &stateFixture{state: tc.state, err: tc.err}, new(releaseFixture), provider)
			if err != nil {
				t.Fatal(err)
			}
			err = c.reconcile(context.Background(), "gateway")
			if tc.name == "deleted" {
				if err != nil || provider.deletes != 1 {
					t.Fatal("retained Gateway cleanup did not run", err)
				}
			} else if err == nil || provider.deletes != 0 {
				t.Fatal("unsafe deletion without current Gateway state", err)
			}

			if provider.creates != 0 || api.updates != 0 {
				t.Fatal("invalid or deleted state changed a live workload")
			}
		})
	}
}

func TestGatewayCleanupRetainsTheSharedDatabaseServer(t *testing.T) {
	gw, release := records(t)
	provider := &providerFixture{err: ErrPending}
	state := &stateFixture{state: &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), Gateway: gw, ResourceVersion: 1, ResourceGeneration: 1, Deleted: true}}
	c, err := New(new(apiFixture), state, &releaseFixture{row: release}, provider)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.reconcile(context.Background(), gw.Metadata.Id); !errors.Is(err, ErrPending) || provider.deletes != 1 {
		t.Fatal("pending SQL cleanup was lost", err)
	}
	provider.err = nil
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || provider.deletes != 2 || !state.state.CleanupTargets["workload"].Targets[testClusterID] {
		t.Fatal("cleanup did not retain the shared server", err)
	}
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || provider.deletes != 3 || provider.sqlDeletes != 0 {
		t.Fatal("late-effect check changed the shared server", err)
	}
}

func TestUnassignedClusterCannotChangeAWorkload(t *testing.T) {
	gw, release := records(t)
	for _, deleted := range []bool{false, true} {
		provider := &providerFixture{unassigned: true, target: "other"}
		api := new(apiFixture)
		c, err := New(api, &stateFixture{state: &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), ResourceVersion: 1, ResourceGeneration: 1, ObservedGeneration: 1, Gateway: gw, Deleted: deleted}}, &releaseFixture{row: release}, provider)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || provider.creates != 0 || provider.deletes != 0 || api.updates != 0 {
			t.Fatal("unassigned Gateway caused a write", err)
		}
	}
}
func TestReadyStatusRequiresProviderSuccess(t *testing.T) {
	gw, release := records(t)
	ready := "Healthy"
	running := "Running"
	gw.Phase = &running
	gw.Status = &ready
	provider := &providerFixture{err: ErrPending}
	api := new(apiFixture)
	c, err := New(api, &stateFixture{state: &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), ResourceVersion: 1, ResourceGeneration: 1, ObservedGeneration: 1, Gateway: gw}}, &releaseFixture{row: release}, provider)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.reconcile(context.Background(), gw.Metadata.Id); !errors.Is(err, ErrPending) || (api.desired != "WorkloadNotReady" || api.phase != "Degraded") {
		t.Fatal("pending workload retained ready state", err)
	}
	gw.Phase = nil
	if err = c.reconcile(context.Background(), gw.Metadata.Id); !errors.Is(err, ErrPending) || api.phase != "Provisioning" {
		t.Fatal("new workload did not remain provisioning", err)
	}
	gw.Phase = &running
	provider.err = errors.New("Kubernetes unavailable")
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err == nil || (api.desired != "WorkloadUnavailable" || api.phase != "Degraded") {
		t.Fatal("failed workload retained ready state", err)
	}
	failed := "error"
	gw.Status = &failed
	provider.err = nil
	api.err = status.Error(codes.Aborted, "conflict")
	if err = c.reconcile(context.Background(), gw.Metadata.Id); status.Code(err) != codes.Aborted {
		t.Fatal("status write failure was lost", err)
	}
	api.err = nil
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || (api.desired != "Healthy" || api.phase != "Running") {
		t.Fatal("status recovery failed", err)
	}
	gw.Status = &ready
	before := api.updates
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || api.updates != before {
		t.Fatal("stable workload emitted an update", err)
	}
	legacy := "ready"
	gw.Status = &legacy
	gw.Phase = nil
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || api.desired != "Healthy" || api.phase != "Running" {
		t.Fatal("legacy health state was not repaired", err)
	}

}

func TestDeletedGatewayCanCleanUpItsFormerCluster(t *testing.T) {
	gw, release := records(t)
	provider := &providerFixture{unassigned: true}
	c, err := New(new(apiFixture), &stateFixture{state: &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), ResourceVersion: 1, ResourceGeneration: 1, ObservedGeneration: 1, Gateway: gw, Deleted: true}}, &releaseFixture{row: release}, provider)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || provider.creates != 0 || provider.deletes != 1 {
		t.Fatal("former cluster retained deleted Gateway", err)
	}
}

type recoveryFixture struct {
	control.GatewayIdentityServiceClient
	pages   [][]string
	cursors []string
	delay   time.Duration
}

func (f *recoveryFixture) ListGatewayReconcileIDs(ctx context.Context, r *control.ListGatewayReconcileIDsRequest, _ ...grpc.CallOption) (*control.ListGatewayReconcileIDsResponse, error) {
	f.cursors = append(f.cursors, r.AfterId)
	if f.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(f.delay):
		}
	}
	page := f.pages[0]
	f.pages = f.pages[1:]
	return &control.ListGatewayReconcileIDsResponse{Ids: page}, nil
}
func TestRecoveryScanRejectsInvalidPages(t *testing.T) {
	id := ksuid.New().String()
	for _, page := range [][]string{{"invalid"}, {id, "invalid"}, {id, ksuid.Nil.String()}, {id, id}, make([]string, 101)} {
		state := &recoveryFixture{pages: [][]string{page}}
		c, _ := New(new(apiFixture), state, new(releaseFixture), new(providerFixture))
		emitted := 0
		if err := c.seed(context.Background(), func(string) error { emitted++; return nil }); err == nil {
			t.Fatal("invalid page was accepted")
		}
		if emitted != 0 {
			t.Fatal("invalid page emitted work before validation", emitted)
		}
	}
}
func TestRecoveryScanAdvancesAcrossPages(t *testing.T) {
	ids := make([]string, 101)
	for i := range ids {
		ids[i] = ksuid.New().String()
	}
	slices.Sort(ids)
	state := &recoveryFixture{pages: [][]string{ids[:100], ids[100:]}}
	c, _ := New(new(apiFixture), state, new(releaseFixture), new(providerFixture))
	queue := make(chan string, QueueCapacity)
	if err := c.seed(context.Background(), func(id string) error { queue <- id; return nil }); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.cursors, []string{"", ids[99]}) {
		t.Fatal("scan did not use the last ID as its cursor")
	}
	for _, id := range ids {
		if got := <-queue; got != id {
			t.Fatal("scan lost an ID")
		}
	}
}

type recoveryWatch struct {
	grpc.ClientStream
	ctx context.Context
}

func (w *recoveryWatch) Header() (metadata.MD, error) { return nil, nil }
func (w *recoveryWatch) Recv() (*pb.WatchGatewaysResponse, error) {
	<-w.ctx.Done()
	return nil, w.ctx.Err()
}

type recoveryAPI struct{ apiFixture }

func (a *recoveryAPI) WatchGateways(ctx context.Context, _ *pb.WatchGatewaysRequest, _ ...grpc.CallOption) (pb.GatewayService_WatchGatewaysClient, error) {
	return &recoveryWatch{ctx: ctx}, nil
}

type slowRecovery struct {
	recoveryFixture
	observed chan string
}

func (s *slowRecovery) GetGatewayIdentityState(_ context.Context, r *control.GetGatewayIdentityStateRequest, _ ...grpc.CallOption) (*control.GetGatewayIdentityStateResponse, error) {
	s.observed <- r.Id
	return &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), ResourceVersion: 1, ResourceGeneration: 1, ObservedGeneration: 1, Deleted: true, Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: r.Id}}}, nil
}
func TestRecoveryScanCanExceedTheResyncInterval(t *testing.T) {
	id := ksuid.New().String()
	state := &slowRecovery{recoveryFixture: recoveryFixture{pages: [][]string{{id}}, delay: ResyncInterval + 100*time.Millisecond}, observed: make(chan string, 1)}
	c, _ := New(new(recoveryAPI), state, new(releaseFixture), new(providerFixture))
	ctx, cancel := context.WithTimeout(context.Background(), ResyncInterval+5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case got := <-state.observed:
		if got != id {
			t.Error("scan changed its ID")
		}
	case err := <-done:
		t.Fatalf("scan ended before it processed its retained ID: %v", err)
	case <-ctx.Done():
		t.Error("long scan did not process its retained ID")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scan did not stop")
	}
}

func TestUnchangedStatusMustConfirmTheCurrentGeneration(t *testing.T) {
	gw, release := records(t)
	healthy, running := "Healthy", "Running"
	gw.Status, gw.Phase = &healthy, &running
	state := &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), Gateway: gw, ResourceVersion: 9, ResourceGeneration: 2, ObservedGeneration: 1}
	api := new(apiFixture)
	provider := new(providerFixture)
	c, err := New(api, &stateFixture{state: state}, &releaseFixture{row: release}, provider)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.reconcile(context.Background(), gw.Metadata.Id); err != nil || api.updates != 1 || provider.creates != 1 {
		t.Fatal("unchanged status skipped a new generation", err)
	}
	state.ObservedGeneration = 2
	if err := c.reconcile(context.Background(), gw.Metadata.Id); err != nil || api.updates != 1 || provider.creates != 2 {
		t.Fatal("current generation bypassed drift repair or repeated status", err)
	}
}

func (f *recoveryFixture) ObserveGatewayCleanup(context.Context, *control.ObserveGatewayCleanupRequest, ...grpc.CallOption) (*control.ObserveGatewayCleanupResponse, error) {
	return &control.ObserveGatewayCleanupResponse{}, nil
}

func TestTargetCleanupRetriesAndChecksLateEffects(t *testing.T) {
	gw, release := records(t)
	provider := &providerFixture{unassigned: true}
	state := &stateFixture{state: &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), ResourceVersion: 1, ResourceGeneration: 1, Gateway: gw, Deleted: true}, conflict: true}
	c, _ := New(new(apiFixture), state, &releaseFixture{row: release}, provider)
	if err := c.reconcile(context.Background(), gw.Metadata.Id); status.Code(err) != codes.Aborted {
		t.Fatal("stale target observation accepted", err)
	}
	state.conflict = false
	if err := c.reconcile(context.Background(), gw.Metadata.Id); err != nil {
		t.Fatal(err)
	}
	provider.err = ErrPending
	if err := c.reconcile(context.Background(), gw.Metadata.Id); !errors.Is(err, ErrPending) || state.state.CleanupTargets["workload"].Targets[testClusterID] || !state.state.CleanupTargets["sql"].Targets[testClusterID] {
		t.Fatal("late workload effect lost its cleanup state", err)
	}
	provider.err = nil
	c, _ = New(new(apiFixture), state, &releaseFixture{row: release}, provider)
	if err := c.reconcile(context.Background(), gw.Metadata.Id); err != nil || !state.state.CleanupTargets["workload"].Targets[testClusterID] || provider.sqlDeletes != 0 {
		t.Fatal("restart repeated SQL cleanup or lost workload cleanup", err)
	}
}

func TestSQLCleanupIsRecordedBeforeWorkloadCompletion(t *testing.T) {
	gw, release := records(t)
	provider := &providerFixture{}
	history := workloadHistory(testClusterID)
	history["sql"].Targets[testClusterID] = false
	state := &stateFixture{state: &control.GetGatewayIdentityStateResponse{CleanupTargets: history, ResourceVersion: 1, ResourceGeneration: 1, Gateway: gw, Deleted: true}, conflict: true}
	c, _ := New(new(apiFixture), state, &releaseFixture{row: release}, provider)
	if err := c.reconcile(context.Background(), gw.Metadata.Id); status.Code(err) != codes.Aborted || provider.sqlDeletes != 1 || history["sql"].Targets[testClusterID] {
		t.Fatal("SQL cleanup lost its uncommitted state", err)
	}
	state.conflict = false
	if err := c.reconcile(context.Background(), gw.Metadata.Id); err != nil || provider.sqlDeletes != 2 || !history["sql"].Targets[testClusterID] || history["workload"].Targets[testClusterID] {
		t.Fatal("SQL cleanup was not recorded separately", err)
	}
	c, _ = New(new(apiFixture), state, &releaseFixture{row: release}, provider)
	if err := c.reconcile(context.Background(), gw.Metadata.Id); err != nil || provider.sqlDeletes != 2 || provider.deletes != 1 || !history["workload"].Targets[testClusterID] {
		t.Fatal("workload cleanup repeated SQL after restart", err)
	}
}

func TestMissingTargetHistoryStopsProviderWork(t *testing.T) {
	gw, release := records(t)
	for _, deleted := range []bool{false, true} {
		provider := new(providerFixture)
		state := &stateFixture{state: &control.GetGatewayIdentityStateResponse{ResourceVersion: 1, ResourceGeneration: 1, Gateway: gw, Deleted: deleted}}
		c, _ := New(new(apiFixture), state, &releaseFixture{row: release}, provider)
		if err := c.reconcile(context.Background(), gw.Metadata.Id); err == nil || provider.creates != 0 || provider.deletes != 0 {
			t.Fatal("missing history reached the provider", err)
		}
	}
}
