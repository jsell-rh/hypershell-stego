package gatewayidentity

import (
	"context"
	"errors"
	"testing"

	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
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
		{name: "denied", err: status.Error(codes.PermissionDenied, "denied")},
		{name: "not found", err: status.Error(codes.NotFound, "absent")},
		{name: "unavailable", err: status.Error(codes.Unavailable, "unavailable")},
		{name: "empty state", state: &control.GetGatewayIdentityStateResponse{}},
		{name: "other resource", state: &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "other"}}, Deleted: true}},
		{name: "explicit deletion", state: &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}}, Deleted: true}, deleted: true},
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
func TestIdentityPublicationRequiresProviderSuccess(t *testing.T) {
	provider := &providerFixture{err: errors.New("provider unavailable")}
	api := new(apiFixture)
	state := &stateFixture{state: &control.GetGatewayIdentityStateResponse{Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: "gateway"}, Name: "gateway"}}}
	controller, _ := New(api, state, provider)
	if err := controller.reconcile(context.Background(), "gateway"); err == nil || api.updates != 0 {
		t.Fatal("failed provider operation published identity")
	}
	provider.err = nil
	api.err = status.Error(codes.Aborted, "transaction conflict")
	if err := controller.reconcile(context.Background(), "gateway"); status.Code(err) != codes.Aborted {
		t.Fatal("API conflict was lost")
	}
	api.err = nil
	if err := controller.reconcile(context.Background(), "gateway"); err != nil || api.updates != 2 {
		t.Fatal("next pass did not recover identity publication")
	}
	identity := "identity"
	state.state.Gateway.Oidc = &identity
	if err := controller.reconcile(context.Background(), "gateway"); err != nil || api.updates != 2 {
		t.Fatal("unchanged identity emitted another update")
	}
}
