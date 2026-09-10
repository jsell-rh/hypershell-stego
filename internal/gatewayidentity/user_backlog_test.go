package gatewayidentity

import (
	"context"
	"fmt"
	"testing"

	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc"
)

type backlogState struct{ progressUserState }

func (*backlogState) ScanGatewayIdentityUsers(_ context.Context, request *control.ScanGatewayIdentityUsersRequest, _ ...grpc.CallOption) (*control.ScanGatewayIdentityUsersResponse, error) {
	start := 0
	if request.AfterGrantId != "" {
		if _, err := fmt.Sscanf(request.AfterGrantId, "grant-%d", &start); err != nil {
			return nil, err
		}
	}
	result := &control.ScanGatewayIdentityUsersResponse{GatewayId: request.GatewayId, AfterGrantId: request.AfterGrantId}
	for i := start + 1; i <= 10100 && len(result.References) < int(request.PageSize); i++ {
		result.References = append(result.References, &control.GatewayIdentityUserReference{GrantId: fmt.Sprintf("grant-%d", i), UserId: fmt.Sprint(i)})
		result.HasMore = i < 10100
	}
	return result, nil
}
func TestUserRecoveryReachesBeyondTenThousandGrants(t *testing.T) {
	provider := &progressUserProvider{providerFixture: new(providerFixture)}
	controller, err := New(new(apiFixture), new(backlogState), provider)
	if err != nil {
		t.Fatal(err)
	}
	if err := controller.reconcileUsers(context.Background(), "gateway"); err == nil || len(provider.subjects) != 10000 {
		t.Fatal("first pass did not yield", err, len(provider.subjects))
	}
	if err := controller.reconcileUsers(context.Background(), "gateway"); err != nil {
		t.Fatal(err)
	}
	if controller.state.(*backlogState).checkpoints["gateway"].After != "" {
		t.Fatal("complete scan retained progress")
	}
	if len(provider.subjects) != 10100 {
		t.Fatal("later grants were not recovered", len(provider.subjects))
	}
}

func TestUserScanSurvivesControllerReplacement(t *testing.T) {
	state := new(backlogState)
	provider := &progressUserProvider{providerFixture: new(providerFixture)}
	first, _ := New(new(apiFixture), state, provider)
	if err := first.reconcileUsers(context.Background(), "gateway"); err == nil || len(provider.subjects) != 10000 {
		t.Fatal("first scan did not yield", err, len(provider.subjects))
	}
	second, _ := New(new(apiFixture), state, provider)
	if err := second.reconcileUsers(context.Background(), "gateway"); err != nil {
		t.Fatal("new controller did not finish the scan", err)
	}
	if len(provider.subjects) != 10100 {
		t.Fatal("controller restart repeated the saved prefix", len(provider.subjects))
	}
}
