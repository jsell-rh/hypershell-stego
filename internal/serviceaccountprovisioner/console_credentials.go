package serviceaccountprovisioner

import (
	"context"
	"time"

	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	provider "github.com/jsell-rh/hypershell-stego/out/keycloak"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type ConsoleCredentialProvider interface {
	GatewayConsoleCredentials(context.Context, string, string, string, int64) (string, provider.Secret, error)
}
type ConsoleCredentialState interface {
	GetGatewayIdentityState(context.Context, *control.GetGatewayIdentityStateRequest, ...grpc.CallOption) (*control.GetGatewayIdentityStateResponse, error)
}

// ConsoleCredentialServer keeps cluster worker grants separate from account
// provisioning grants. The generated transport verifies each caller's token.
type ConsoleCredentialServer struct {
	pb.UnimplementedGatewayConsoleCredentialServiceServer
	provider ConsoleCredentialProvider
	state    ConsoleCredentialState
	policy   *auth.GrantPolicy
}

func NewConsoleCredentialServer(provider ConsoleCredentialProvider, state ConsoleCredentialState, policy *auth.GrantPolicy) *ConsoleCredentialServer {
	return &ConsoleCredentialServer{provider: provider, state: state, policy: policy}
}
func (s *ConsoleCredentialServer) GetCredentials(ctx context.Context, request *pb.GatewayConsoleCredentialRequest) (*pb.GatewayConsoleCredentialResponse, error) {
	if s == nil || !s.policy.Allows(auth.IdentityFromContext(ctx), "Gateway", "read.console-credential", request.GetClusterId()) {
		return nil, status.Error(codes.PermissionDenied, "console credential caller is not allowed")
	}
	validID := func(id string) bool {
		parsed, err := ksuid.Parse(id)
		return err == nil && parsed != ksuid.Nil && parsed.String() == id
	}
	if request == nil || len(request.ProtoReflect().GetUnknown()) != 0 || !validID(request.GatewayId) || !validID(request.ClusterId) || request.ResourceVersion < 1 {
		return nil, status.Error(codes.InvalidArgument, "console credential requires a current Gateway and cluster observation")
	}
	if s.provider == nil || s.state == nil {
		return nil, status.Error(codes.Unavailable, "console credential provider is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	read := func() (*control.GetGatewayIdentityStateResponse, error) {
		state, err := s.state.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: request.GatewayId})
		if err != nil {
			return nil, status.Error(codes.Unavailable, "console credential observation is unavailable")
		}
		gateway := state.GetGateway()
		if state == nil || len(state.ProtoReflect().GetUnknown()) != 0 || state.Deleted || state.ResourceVersion != request.ResourceVersion || gateway.GetMetadata().GetId() != request.GatewayId || gateway.GetClusterId() != request.ClusterId || gateway.GetName() == "" {
			return nil, status.Error(codes.FailedPrecondition, "console credential observation changed")
		}
		return state, nil
	}
	before, err := read()
	if err != nil {
		return nil, err
	}
	clientID, secret, err := s.provider.GatewayConsoleCredentials(ctx, request.GatewayId, before.Gateway.Name, request.ClusterId, request.ResourceVersion)
	if err != nil {
		return nil, status.Error(codes.Unavailable, "console credential is not ready")
	}
	after, err := read()
	if err != nil {
		return nil, err
	}
	if before.Gateway.Name != after.Gateway.Name {
		return nil, status.Error(codes.FailedPrecondition, "console credential observation changed")
	}
	if err := ctx.Err(); err != nil {
		return nil, status.FromContextError(err).Err()
	}
	if deadline, ok := ctx.Deadline(); !ok || !time.Now().Before(deadline) {
		return nil, status.Error(codes.DeadlineExceeded, "console credential read timed out")
	}
	if clientID != "hs-console-"+request.GatewayId || secret.Reveal() == "" {
		return nil, status.Error(codes.Unavailable, "console credential is not ready")
	}
	return &pb.GatewayConsoleCredentialResponse{ClientId: clientID, ClientSecret: secret.Reveal()}, nil
}
