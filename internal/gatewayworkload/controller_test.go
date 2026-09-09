package gatewayworkload

import (
	"context"
	"errors"
	"github.com/segmentio/ksuid"
	"slices"
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
	state *control.GetGatewayIdentityStateResponse
	err   error
}

func (f *stateFixture) GetGatewayIdentityState(context.Context, *control.GetGatewayIdentityStateRequest, ...grpc.CallOption) (*control.GetGatewayIdentityStateResponse, error) {
	return f.state, f.err
}

type apiFixture struct {
	pb.GatewayServiceClient
	updates int
	desired string
	err     error
}

func (f *apiFixture) UpdateGateway(_ context.Context, r *pb.UpdateGatewayRequest, _ ...grpc.CallOption) (*pb.UpdateGatewayResponse, error) {
	f.updates++
	f.desired = r.GetStatus()
	return &pb.UpdateGatewayResponse{}, f.err
}

type databaseFixture struct {
	pb.ManagedDatabaseServiceClient
	row       *pb.ManagedDatabase
	err       error
	deletes   int
	deleteErr error
}

func (f *databaseFixture) DeleteManagedDatabase(context.Context, *pb.DeleteManagedDatabaseRequest, ...grpc.CallOption) (*pb.DeleteManagedDatabaseResponse, error) {
	f.deletes++
	return &pb.DeleteManagedDatabaseResponse{}, f.deleteErr
}

func (f *databaseFixture) GetManagedDatabase(context.Context, *pb.GetManagedDatabaseRequest, ...grpc.CallOption) (*pb.GetManagedDatabaseResponse, error) {
	return &pb.GetManagedDatabaseResponse{ManagedDatabase: f.row}, f.err
}

type releaseFixture struct {
	pb.GatewayReleaseServiceClient
	row *pb.GatewayRelease
}

func (f *releaseFixture) GetGatewayRelease(context.Context, *pb.GetGatewayReleaseRequest, ...grpc.CallOption) (*pb.GetGatewayReleaseResponse, error) {
	return &pb.GetGatewayReleaseResponse{GatewayRelease: f.row}, nil
}

type providerFixture struct {
	owned            bool
	unassigned       bool
	creates, deletes int
	err              error
}

func (f *providerFixture) Handles(*pb.Gateway) bool                        { return !f.unassigned }
func (f *providerFixture) Owns(context.Context, *pb.Gateway) (bool, error) { return f.owned, nil }

func (f *providerFixture) Ensure(context.Context, *pb.Gateway, *pb.ManagedDatabase, *pb.GatewayRelease) error {
	f.creates++
	return f.err
}
func (f *providerFixture) Delete(context.Context, string) error         { f.deletes++; return f.err }
func (f *providerFixture) GatewayIDs(context.Context) ([]string, error) { return nil, nil }

func TestDeletionRequiresExplicitCurrentState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   *control.GetGatewayIdentityStateResponse
		err     error
		deleted bool
	}{
		{name: "denied", err: status.Error(codes.PermissionDenied, "denied")},
		{name: "absent", err: status.Error(codes.NotFound, "absent")},
		{name: "empty", state: &control.GetGatewayIdentityStateResponse{}},
		{name: "mismatch", state: &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "other"}}, Deleted: true}},
		{name: "deleted", state: &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}}, Deleted: true}, deleted: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			provider := new(providerFixture)
			api := new(apiFixture)
			c, err := New(api, &stateFixture{state: tc.state, err: tc.err}, &databaseFixture{err: status.Error(codes.NotFound, "already deleted")}, new(releaseFixture), provider)
			if err != nil {
				t.Fatal(err)
			}
			err = c.reconcile(context.Background(), "gateway")
			if tc.deleted {
				if err != nil || provider.deletes != 1 {
					t.Fatal("explicit deletion failed", err)
				}
			} else if err == nil || provider.deletes != 0 {
				t.Fatal("unsafe deletion", err)
			}
			if provider.creates != 0 || api.updates != 0 {
				t.Fatal("invalid or deleted state changed a live workload")
			}
		})
	}
}

func TestDatabaseDeletionCanRetryAfterGatewayRemoval(t *testing.T) {
	gw, db, release := records(t)
	provider := new(providerFixture)
	database := &databaseFixture{row: db, deleteErr: status.Error(codes.Unavailable, "retry")}
	c, err := New(new(apiFixture), &stateFixture{state: &control.GetGatewayIdentityStateResponse{Gateway: gw, Deleted: true}}, database, &releaseFixture{row: release}, provider)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.reconcile(context.Background(), gw.Metadata.Id); status.Code(err) != codes.Unavailable || database.deletes != 1 || provider.deletes != 1 {
		t.Fatal("database delete failure was lost", err)
	}
	database.deleteErr = nil
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || database.deletes != 2 || provider.deletes != 2 {
		t.Fatal("database delete did not retry", err)
	}
	provider.err = ErrPending
	if err = c.reconcile(context.Background(), gw.Metadata.Id); !errors.Is(err, ErrPending) || database.deletes != 2 {
		t.Fatal("database deleted before Gateway removal", err)
	}
}

func TestUnassignedClusterCannotChangeAWorkload(t *testing.T) {
	gw, db, release := records(t)
	for _, deleted := range []bool{false, true} {
		provider := &providerFixture{unassigned: true}
		database := &databaseFixture{row: db}
		api := new(apiFixture)
		c, err := New(api, &stateFixture{state: &control.GetGatewayIdentityStateResponse{Gateway: gw, Deleted: deleted}}, database, &releaseFixture{row: release}, provider)
		if err != nil {
			t.Fatal(err)
		}
		if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || provider.creates != 0 || provider.deletes != 0 || database.deletes != 0 || api.updates != 0 {
			t.Fatal("unassigned Gateway caused a write", err)
		}
	}
}
func TestReadyStatusRequiresProviderSuccess(t *testing.T) {
	gw, db, release := records(t)
	ready := "ready"
	gw.Status = &ready
	provider := &providerFixture{err: ErrPending}
	api := new(apiFixture)
	database := &databaseFixture{row: db}
	c, err := New(api, &stateFixture{state: &control.GetGatewayIdentityStateResponse{Gateway: gw}}, database, &releaseFixture{row: release}, provider)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.reconcile(context.Background(), gw.Metadata.Id); !errors.Is(err, ErrPending) || api.desired != "provisioning" {
		t.Fatal("pending workload retained ready state", err)
	}
	provider.err = errors.New("Kubernetes unavailable")
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err == nil || api.desired != "error" {
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
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || api.desired != "ready" {
		t.Fatal("status recovery failed", err)
	}
	gw.Status = &ready
	before := api.updates
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || api.updates != before {
		t.Fatal("stable workload emitted an update", err)
	}
	database.err = status.Error(codes.NotFound, "database missing")
	before = provider.creates
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err == nil || api.desired != "error" || provider.creates != before {
		t.Fatal("missing database reached provider", err)
	}
}

func TestDeletedGatewayCanCleanUpItsFormerCluster(t *testing.T) {
	gw, db, release := records(t)
	provider := &providerFixture{unassigned: true, owned: true}
	database := &databaseFixture{row: db}
	c, err := New(new(apiFixture), &stateFixture{state: &control.GetGatewayIdentityStateResponse{Gateway: gw, Deleted: true}}, database, &releaseFixture{row: release}, provider)
	if err != nil {
		t.Fatal(err)
	}
	if err = c.reconcile(context.Background(), gw.Metadata.Id); err != nil || provider.creates != 0 || provider.deletes != 1 || database.deletes != 1 {
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
	for _, page := range [][]string{{"invalid"}, {id, id}, make([]string, 101)} {
		state := &recoveryFixture{pages: [][]string{page}}
		c, _ := New(new(apiFixture), state, new(databaseFixture), new(releaseFixture), new(providerFixture))
		if err := c.seed(context.Background(), make(chan string, QueueCapacity)); err == nil {
			t.Fatal("invalid page was accepted")
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
	c, _ := New(new(apiFixture), state, new(databaseFixture), new(releaseFixture), new(providerFixture))
	queue := make(chan string, QueueCapacity)
	if err := c.seed(context.Background(), queue); err != nil {
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
	return &control.GetGatewayIdentityStateResponse{Deleted: true, Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: r.Id}}}, nil
}
func TestRecoveryScanCanExceedTheResyncInterval(t *testing.T) {
	id := ksuid.New().String()
	state := &slowRecovery{recoveryFixture: recoveryFixture{pages: [][]string{{id}}, delay: ResyncInterval + 100*time.Millisecond}, observed: make(chan string, 1)}
	c, _ := New(new(recoveryAPI), state, &databaseFixture{err: status.Error(codes.NotFound, "absent")}, new(releaseFixture), new(providerFixture))
	ctx, cancel := context.WithTimeout(context.Background(), ResyncInterval+5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.session(ctx) }()
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
