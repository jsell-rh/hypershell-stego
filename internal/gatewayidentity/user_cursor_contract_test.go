package gatewayidentity

import (
	"context"
	"errors"
	"testing"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type invalidUserCursorState struct {
	progressUserState
	response *control.ScanGatewayIdentityUsersResponse
	err      error
}

func (s *invalidUserCursorState) ScanGatewayIdentityUsers(context.Context, *control.ScanGatewayIdentityUsersRequest, ...grpc.CallOption) (*control.ScanGatewayIdentityUsersResponse, error) {
	return s.response, s.err
}
func TestUserCursorRejectsMalformedPagesBeforeProviderWork(t *testing.T) {
	for _, response := range []*control.ScanGatewayIdentityUsersResponse{
		nil,
		{GatewayId: "other"},
		{GatewayId: "gateway", AfterGrantId: "unexpected"},
		{GatewayId: "gateway", HasMore: true},
		{GatewayId: "gateway", References: []*control.GatewayIdentityUserReference{nil}},
		{GatewayId: "gateway", References: []*control.GatewayIdentityUserReference{{GrantId: "a", UserId: "first"}, {GrantId: "a", UserId: "second"}}},
		{GatewayId: "gateway", References: []*control.GatewayIdentityUserReference{{GrantId: "a", UserId: "first"}, {GrantId: "b"}}},
	} {
		provider := &progressUserProvider{providerFixture: new(providerFixture)}
		controller, err := New(new(apiFixture), &invalidUserCursorState{response: response}, provider)
		if err != nil {
			t.Fatal(err)
		}
		if err := controller.reconcileUsers(context.Background(), "gateway"); !errors.Is(err, runtime.ErrScanContract) || len(provider.subjects) != 0 {
			t.Fatal("invalid page caused provider work", err, provider.subjects)
		}
	}
	for _, code := range []codes.Code{codes.Unimplemented, codes.PermissionDenied, codes.Unavailable} {
		provider := &progressUserProvider{providerFixture: new(providerFixture)}
		controller, _ := New(new(apiFixture), &invalidUserCursorState{err: status.Error(code, "private remote details")}, provider)
		err := controller.reconcileUsers(context.Background(), "gateway")
		if code == codes.Unimplemented && !errors.Is(err, runtime.ErrScanContract) || code != codes.Unimplemented && status.Code(err) != code || len(provider.subjects) != 0 {
			t.Fatal("RPC failure contract changed", code, err)
		}
	}
}

func TestUserCursorCombinesRepeatedUsersWithinOnePage(t *testing.T) {
	response := &control.ScanGatewayIdentityUsersResponse{GatewayId: "gateway", References: []*control.GatewayIdentityUserReference{{GrantId: "a", UserId: "first"}, {GrantId: "b", UserId: "first"}}}
	provider := &progressUserProvider{providerFixture: new(providerFixture)}
	controller, _ := New(new(apiFixture), &invalidUserCursorState{response: response}, provider)
	for pass := 1; pass <= 2; pass++ {
		if err := controller.reconcileUsers(context.Background(), "gateway"); err != nil {
			t.Fatal(err)
		}
		if len(provider.subjects) != pass {
			t.Fatal("duplicate user work or skipped full scan", provider.subjects)
		}
	}
}

type missingCycleRevision struct{ progressUserState }

func (*missingCycleRevision) LoadGatewayIdentityCycle(_ context.Context, request *control.LoadGatewayIdentityCheckpointRequest, _ ...grpc.CallOption) (*control.GatewayIdentityCycle, error) {
	return &control.GatewayIdentityCycle{GatewayId: request.GatewayId, ResourceGeneration: 1}, nil
}
func TestCycleWithoutResourceRevisionStopsBeforeProviderWork(t *testing.T) {
	provider := &progressUserProvider{providerFixture: new(providerFixture)}
	controller, _ := New(new(apiFixture), new(missingCycleRevision), provider)
	if err := controller.reconcileUsers(context.Background(), "gateway"); !errors.Is(err, runtime.ErrScanContract) || len(provider.subjects) != 0 {
		t.Fatal("old cycle contract permitted provider work", err, provider.subjects)
	}
}
