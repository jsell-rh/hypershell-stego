package grpcapi

import (
	"context"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	transport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func providerStateResponse(id string, kind pb.GatewayIdentityClientKind, value gateways.GatewayProviderState) *pb.GatewayProviderState {
	return &pb.GatewayProviderState{GatewayId: id, Version: value.State.Version, SealedState: value.State.Data, ResourceVersion: value.ResourceVersion, Deleted: value.Deleted, ClientKind: kind}
}
func (s *identityServer) LoadGatewayProviderState(ctx context.Context, request *pb.LoadGatewayProviderStateRequest) (*pb.GatewayProviderState, error) {
	if request == nil || len(request.ProtoReflect().GetUnknown()) != 0 || !validIdentityClientKind(request.GetClientKind()) {
		return nil, status.Error(codes.InvalidArgument, "request contains unsupported fields")
	}
	load := s.service.LoadIdentityProviderState
	if request.ClientKind == pb.GatewayIdentityClientKind_GATEWAY_IDENTITY_CLIENT_KIND_CONSOLE {
		load = s.service.LoadConsoleIdentityProviderState
	}
	value, err := load(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId())
	if err != nil {
		return nil, mapError(err)
	}
	return providerStateResponse(request.GetGatewayId(), request.GetClientKind(), value), nil
}
func (s *identityServer) SaveGatewayProviderState(ctx context.Context, request *pb.SaveGatewayProviderStateRequest) (*pb.GatewayProviderState, error) {
	if request == nil || len(request.ProtoReflect().GetUnknown()) != 0 || !validIdentityClientKind(request.GetClientKind()) {
		return nil, status.Error(codes.InvalidArgument, "request contains unsupported fields")
	}
	version, present, err := transport.ResourceVersion(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, mapError(gateways.ErrObservationRequired)
	}
	save := s.service.SaveIdentityProviderState
	if request.ClientKind == pb.GatewayIdentityClientKind_GATEWAY_IDENTITY_CLIENT_KIND_CONSOLE {
		save = s.service.SaveConsoleIdentityProviderState
	}
	value, err := save(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), version, request.GetExpectedVersion(), request.GetSealedState(), request.GetCleanup())
	if err != nil {
		return nil, mapError(err)
	}
	return providerStateResponse(request.GetGatewayId(), request.GetClientKind(), value), nil
}

func validIdentityClientKind(kind pb.GatewayIdentityClientKind) bool {
	return kind == pb.GatewayIdentityClientKind_GATEWAY_IDENTITY_CLIENT_KIND_NATIVE || kind == pb.GatewayIdentityClientKind_GATEWAY_IDENTITY_CLIENT_KIND_CONSOLE
}
