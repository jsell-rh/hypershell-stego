package gatewayidentity

import (
	"context"
	"errors"
	"strconv"
	"testing"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type stateFixture struct {
	checkpointFixture
	state                *control.GetGatewayIdentityStateResponse
	err                  error
	observations         int
	conflict             bool
	identityObservations int
	identityError        error
}

func (f *stateFixture) GetGatewayIdentityState(context.Context, *control.GetGatewayIdentityStateRequest, ...grpc.CallOption) (*control.GetGatewayIdentityStateResponse, error) {
	return f.state, f.err
}

type apiFixture struct {
	pb.GatewayServiceClient
	updates int
	err     error
}

func (f *apiFixture) UpdateGateway(context.Context, *pb.UpdateGatewayRequest, ...grpc.CallOption) (*pb.UpdateGatewayResponse, error) {
	f.updates++
	return &pb.UpdateGatewayResponse{}, f.err
}

type providerFixture struct {
	creates, deletes int
	err              error
}

func (f *providerFixture) EnsureGateway(context.Context, string, string) (string, error) {
	f.creates++
	return "identity", f.err
}
func (f *providerFixture) DeleteGateway(context.Context, string) error  { f.deletes++; return f.err }
func (f *providerFixture) GatewayIDs(context.Context) ([]string, error) { return nil, nil }

func TestDeletionRequiresExplicitPrivilegedState(t *testing.T) {
	for _, test := range []struct {
		name    string
		state   *control.GetGatewayIdentityStateResponse
		err     error
		deleted bool
	}{
		{name: "missing revision", state: &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}}, Deleted: true}},
		{name: "denied", err: status.Error(codes.PermissionDenied, "denied")},
		{name: "not found", err: status.Error(codes.NotFound, "absent")},
		{name: "unavailable", err: status.Error(codes.Unavailable, "unavailable")},
		{name: "empty state", state: &control.GetGatewayIdentityStateResponse{ResourceVersion: 1}},
		{name: "other resource", state: &control.GetGatewayIdentityStateResponse{ResourceVersion: 1, Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "other"}}, Deleted: true}},
		{name: "explicit deletion", state: &control.GetGatewayIdentityStateResponse{Cleanup: map[string]bool{"identity": false}, ResourceVersion: 1, Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}}, Deleted: true}, deleted: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := new(providerFixture)
			api := new(apiFixture)
			controller, _ := New(api, &stateFixture{state: test.state, err: test.err}, provider)
			err := controller.reconcile(context.Background(), "gateway")
			if test.deleted {
				if err != nil || provider.deletes != 1 {
					t.Fatalf("explicit deletion: %v", err)
				}
			} else if err == nil || provider.deletes != 0 {
				t.Fatalf("unsafe deletion: %v", err)
			}
			if provider.creates != 0 || api.updates != 0 {
				t.Fatal("invalid state changed a live identity")
			}
		})
	}
}
func (f *stateFixture) ObserveGatewayIdentity(ctx context.Context, request *control.ObserveGatewayIdentityRequest, _ ...grpc.CallOption) (*control.ObserveGatewayIdentityResponse, error) {
	f.identityObservations++
	md, _ := metadata.FromOutgoingContext(ctx)
	version := md.Get("if-resource-version")
	if len(version) != 1 || version[0] != strconv.FormatInt(f.state.ResourceVersion, 10) || request.Id != f.state.Gateway.Metadata.Id {
		return nil, status.Error(codes.InvalidArgument, "invalid observation")
	}
	if f.identityError != nil {
		return nil, f.identityError
	}
	conditionStatus := "Unknown"
	if request.Oidc != nil {
		f.state.Gateway.Oidc = request.Oidc
		conditionStatus = "True"
	}
	f.state.Conditions["identity"] = &control.ResourceConditions{Conditions: map[string]*control.ResourceCondition{"ClientReady": {Status: conditionStatus, Reason: request.Reason, ObservedGeneration: f.state.ResourceGeneration, Current: true}}}
	f.state.ResourceVersion++
	return &control.ObserveGatewayIdentityResponse{}, nil
}
func TestIdentityPublicationRequiresProviderSuccess(t *testing.T) {
	provider := &providerFixture{err: errors.New("provider unavailable")}
	api := new(apiFixture)
	state := &stateFixture{state: &control.GetGatewayIdentityStateResponse{ResourceVersion: 1, ResourceGeneration: 1, Conditions: map[string]*control.ResourceConditions{"identity_users": {Conditions: map[string]*control.ResourceCondition{"GrantsSynchronized": {Status: "Unknown", Reason: "ObservationPending"}}}, "identity": {Conditions: map[string]*control.ResourceCondition{"ClientReady": {Status: "Unknown", Reason: "ObservationPending"}}}}, Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}, Name: "gateway"}}}
	controller, _ := New(api, state, provider)
	if err := controller.reconcile(context.Background(), "gateway"); err == nil || state.state.Gateway.Oidc != nil || state.identityObservations != 1 {
		t.Fatal("failed provider operation published identity", err)
	}
	if condition := state.state.Conditions["identity"].Conditions["ClientReady"]; condition.Status != "Unknown" || condition.Reason != "IdentityProviderUnavailable" {
		t.Fatal("failure condition was lost", condition)
	}
	provider.err = nil
	state.identityError = status.Error(codes.Aborted, "transaction conflict")
	if err := controller.reconcile(context.Background(), "gateway"); status.Code(err) != codes.Aborted {
		t.Fatal("API conflict was lost", err)
	}
	state.identityError = nil
	if err := controller.reconcile(context.Background(), "gateway"); err != nil || state.identityObservations != 3 {
		t.Fatal("identity publication did not recover", err)
	}
	if err := controller.reconcile(context.Background(), "gateway"); err != nil || state.identityObservations != 3 || api.updates != 0 {
		t.Fatal("unchanged identity emitted another update", err)
	}
}

func (f *stateFixture) ScanGatewayIdentityUsers(_ context.Context, request *control.ScanGatewayIdentityUsersRequest, _ ...grpc.CallOption) (*control.ScanGatewayIdentityUsersResponse, error) {
	return fixtureUserPage(request), nil
}
func (f *providerFixture) ReconcileGatewayUser(context.Context, string, string, string, string) error {
	return f.err
}

type userStateFixture struct {
	*stateFixture
	response *control.GetGatewayIdentityUserResponse
	failure  error
}

func (f *userStateFixture) ScanGatewayIdentityUsers(_ context.Context, request *control.ScanGatewayIdentityUsersRequest, _ ...grpc.CallOption) (*control.ScanGatewayIdentityUsersResponse, error) {
	return fixtureUserPage(request, "user"), nil
}
func (f *userStateFixture) GetGatewayIdentityUser(context.Context, *control.GetGatewayIdentityUserRequest, ...grpc.CallOption) (*control.GetGatewayIdentityUserResponse, error) {
	return f.response, f.failure
}

type userProviderFixture struct {
	*providerFixture
	writes int
	role   string
}

func (f *userProviderFixture) ReconcileGatewayUser(_ context.Context, _, _, _, role string) error {
	f.writes++
	f.role = role
	return nil
}

func TestUserRoleChangesRequireCurrentMatchingState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   *control.GetGatewayIdentityUserResponse
		failure error
	}{
		{name: "denied", failure: status.Error(codes.PermissionDenied, "denied")},
		{name: "missing", failure: status.Error(codes.NotFound, "absent")},
		{name: "empty"},
		{name: "other Gateway", state: &control.GetGatewayIdentityUserResponse{GatewayId: "other", UserId: "user"}},
		{name: "other user", state: &control.GetGatewayIdentityUserResponse{GatewayId: "gateway", UserId: "other"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := &userStateFixture{stateFixture: new(stateFixture), response: tc.state, failure: tc.failure}
			provider := &userProviderFixture{providerFixture: new(providerFixture)}
			controller, _ := New(new(apiFixture), state, provider)
			if err := controller.reconcileUsers(context.Background(), "gateway"); err == nil || provider.writes != 0 {
				t.Fatal("invalid user state reached the provider", err)
			}
		})
	}
	state := &userStateFixture{stateFixture: new(stateFixture), response: &control.GetGatewayIdentityUserResponse{GatewayId: "gateway", UserId: "user", Issuer: "https://issuer.example", Subject: "subject", Role: "gateway:owner"}}
	provider := &userProviderFixture{providerFixture: new(providerFixture)}
	controller, _ := New(new(apiFixture), state, provider)
	if err := controller.reconcileUsers(context.Background(), "gateway"); err != nil || provider.role != "gateway:owner" {
		t.Fatal("owner mapping", err)
	}
	state.response.Role = "gateway:viewer"
	if err := controller.reconcileUsers(context.Background(), "gateway"); err != nil || provider.role != "gateway:viewer" {
		t.Fatal("stale owner mapping", err)
	}
	state.response.Role = ""
	if err := controller.reconcileUsers(context.Background(), "gateway"); err != nil || provider.role != "" {
		t.Fatal("removed grant was ignored", err)
	}
}

type progressUserState struct {
	checkpointFixture
}

func (*progressUserState) ScanGatewayIdentityUsers(_ context.Context, request *control.ScanGatewayIdentityUsersRequest, _ ...grpc.CallOption) (*control.ScanGatewayIdentityUsersResponse, error) {
	return fixtureUserPage(request, "first", "second"), nil
}
func (*progressUserState) GetGatewayIdentityUser(_ context.Context, request *control.GetGatewayIdentityUserRequest, _ ...grpc.CallOption) (*control.GetGatewayIdentityUserResponse, error) {
	return &control.GetGatewayIdentityUserResponse{GatewayId: request.GatewayId, UserId: request.UserId, Issuer: "https://issuer.example", Subject: request.UserId, Role: "gateway:viewer"}, nil
}

type progressUserProvider struct {
	*providerFixture
	subjects []string
	cancel   context.CancelFunc
}

func (f *progressUserProvider) ReconcileGatewayUser(_ context.Context, _, _, subject, _ string) error {
	f.subjects = append(f.subjects, subject)
	if f.cancel != nil {
		f.cancel()
	}
	return nil
}

func TestUserScanRepeatsUncommittedWorkAfterParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &progressUserProvider{providerFixture: new(providerFixture), cancel: cancel}
	controller, _ := New(new(apiFixture), new(progressUserState), provider)
	if err := controller.reconcileUsers(ctx, "gateway"); !errors.Is(err, context.Canceled) {
		t.Fatal("first pass did not stop", err)
	}
	provider.cancel = nil
	if err := controller.reconcileUsers(context.Background(), "gateway"); err != nil {
		t.Fatal(err)
	}
	if len(provider.subjects) != 3 || provider.subjects[0] != "first" || provider.subjects[1] != "first" || provider.subjects[2] != "second" {
		t.Fatal("later users starved", provider.subjects)
	}
	if state, err := runtime.DecodeCycle(controller.state.(*progressUserState).cycles["gateway"].After); err != nil || !state.Complete || state.Failed {
		t.Fatal("completed scan lost its outcome", state, err)
	}
}

type lastUserProvider struct {
	*providerFixture
	cancel                  context.CancelFunc
	firstCalls, secondCalls int
}

func (f *lastUserProvider) ReconcileGatewayUser(ctx context.Context, _, _, subject, _ string) error {
	if subject == "first" {
		f.firstCalls++
		return nil
	}
	f.secondCalls++
	if f.secondCalls == 1 {
		f.cancel()
		return ctx.Err()
	}
	return nil
}
func TestUserScanRetriesFailedItemAfterParentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	provider := &lastUserProvider{providerFixture: new(providerFixture), cancel: cancel}
	controller, _ := New(new(apiFixture), new(progressUserState), provider)
	if err := controller.reconcileUsers(ctx, "gateway"); !errors.Is(err, context.Canceled) {
		t.Fatal("first pass did not report timeout", err)
	}
	if err := controller.reconcileUsers(context.Background(), "gateway"); err != nil {
		t.Fatal(err)
	}
	if provider.firstCalls != 2 || provider.secondCalls != 2 {
		t.Fatalf("last user lost its retry position: first=%d second=%d", provider.firstCalls, provider.secondCalls)
	}
}

func (f *stateFixture) ObserveGatewayCleanup(ctx context.Context, request *control.ObserveGatewayCleanupRequest, _ ...grpc.CallOption) (*control.ObserveGatewayCleanupResponse, error) {
	f.observations++
	md, _ := metadata.FromOutgoingContext(ctx)
	version := md.Get("if-resource-version")
	if len(version) != 1 || version[0] != "1" || request.Owner != "identity" || request.Id != f.state.Gateway.Metadata.Id {
		return nil, status.Error(codes.InvalidArgument, "invalid observation")
	}
	if f.conflict {
		return nil, status.Error(codes.Aborted, "revision changed")
	}
	f.state.Cleanup[request.Owner] = request.Complete
	return &control.ObserveGatewayCleanupResponse{}, nil
}

func TestIdentityCleanupRequiresFreshProviderWork(t *testing.T) {
	provider := new(providerFixture)
	state := &stateFixture{state: &control.GetGatewayIdentityStateResponse{ResourceVersion: 1, Deleted: true, Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}}, Cleanup: map[string]bool{"identity": false}}, conflict: true}
	controller, _ := New(new(apiFixture), state, provider)
	if err := controller.reconcile(context.Background(), "gateway"); status.Code(err) != codes.Aborted || provider.deletes != 1 || state.state.Cleanup["identity"] {
		t.Fatal("stale completion was accepted", err)
	}
	state.conflict = false
	if err := controller.reconcile(context.Background(), "gateway"); err != nil || provider.deletes != 2 || !state.state.Cleanup["identity"] {
		t.Fatal("retry did not repeat provider work", err)
	}
	if err := controller.reconcile(context.Background(), "gateway"); err != nil || provider.deletes != 3 || state.observations != 2 {
		t.Fatal("completed cleanup stopped checks or repeated its write", err)
	}
	provider.err = errors.New("late provider effect")
	if err := controller.reconcile(context.Background(), "gateway"); err == nil || state.state.Cleanup["identity"] || state.observations != 3 {
		t.Fatal("failed cleanup did not reopen the observation", err)
	}
	provider.err = nil
	controller, _ = New(new(apiFixture), state, provider)
	if err := controller.reconcile(context.Background(), "gateway"); err != nil || !state.state.Cleanup["identity"] || provider.deletes != 5 {
		t.Fatal("new controller did not recover pending cleanup", err)
	}
}

func TestMissingCleanupDeclarationStopsIdentityDeletion(t *testing.T) {
	provider := new(providerFixture)
	state := &stateFixture{state: &control.GetGatewayIdentityStateResponse{ResourceVersion: 1, Deleted: true, Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}}}}
	controller, _ := New(new(apiFixture), state, provider)
	if err := controller.reconcile(context.Background(), "gateway"); err == nil || provider.deletes != 0 {
		t.Fatal("missing cleanup declaration permitted deletion", err)
	}
}

func fixtureUserPage(request *control.ScanGatewayIdentityUsersRequest, ids ...string) *control.ScanGatewayIdentityUsersResponse {
	result := &control.ScanGatewayIdentityUsersResponse{GatewayId: request.GatewayId, AfterGrantId: request.AfterGrantId}
	start := request.AfterGrantId == ""
	for _, id := range ids {
		if start {
			result.References = append(result.References, &control.GatewayIdentityUserReference{GrantId: id, UserId: id})
		} else if id == request.AfterGrantId {
			start = true
		}
	}
	return result
}

func TestMissingIdentityConditionContractStopsProviderWork(t *testing.T) {
	provider := new(providerFixture)
	state := &stateFixture{state: &control.GetGatewayIdentityStateResponse{ResourceVersion: 1, ResourceGeneration: 1, Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}}}}
	controller, _ := New(new(apiFixture), state, provider)
	if err := controller.reconcile(context.Background(), "gateway"); err == nil || provider.creates != 0 {
		t.Fatal("missing condition contract permitted provider work", err)
	}
}

func TestMissingGrantConditionContractStopsProviderWork(t *testing.T) {
	for _, test := range []struct {
		name  string
		group *control.ResourceConditions
	}{
		{name: "missing owner"},
		{name: "missing condition", group: &control.ResourceConditions{Conditions: map[string]*control.ResourceCondition{}}},
		{name: "nil condition", group: &control.ResourceConditions{Conditions: map[string]*control.ResourceCondition{"GrantsSynchronized": nil}}},
	} {
		t.Run(test.name, func(t *testing.T) {
			provider := new(providerFixture)
			state := &stateFixture{state: &control.GetGatewayIdentityStateResponse{
				ResourceVersion: 1, ResourceGeneration: 1,
				Gateway:    &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}},
				Conditions: map[string]*control.ResourceConditions{"identity": {Conditions: map[string]*control.ResourceCondition{"ClientReady": {Status: "Unknown"}}}},
			}}
			if test.group != nil {
				state.state.Conditions["identity_users"] = test.group
			}
			controller, err := New(new(apiFixture), state, provider)
			if err != nil {
				t.Fatal(err)
			}
			if err := controller.reconcile(context.Background(), "gateway"); !errors.Is(err, runtime.ErrObservationContract) || provider.creates != 0 || provider.deletes != 0 || state.identityObservations != 0 {
				t.Fatal("missing grant condition contract permitted provider work", err)
			}
		})
	}
}
