package grpcapi

import (
	"context"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	transport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func providerStateResponse(id string, value gateways.GatewayProviderState) *pb.GatewayProviderState {
	return &pb.GatewayProviderState{GatewayId: id, Version: value.State.Version, SealedState: value.State.Data, ResourceVersion: value.ResourceVersion, Deleted: value.Deleted}
}
func (s *identityServer) LoadGatewayProviderState(ctx context.Context, request *pb.LoadGatewayProviderStateRequest) (*pb.GatewayProviderState, error) {
	if request == nil || len(request.ProtoReflect().GetUnknown()) != 0 {
		return nil, status.Error(codes.InvalidArgument, "request contains unsupported fields")
	}
	value, err := s.service.LoadIdentityProviderState(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId())
	if err != nil {
		return nil, mapError(err)
	}
	return providerStateResponse(request.GetGatewayId(), value), nil
}
func (s *identityServer) SaveGatewayProviderState(ctx context.Context, request *pb.SaveGatewayProviderStateRequest) (*pb.GatewayProviderState, error) {
	if request == nil || len(request.ProtoReflect().GetUnknown()) != 0 {
		return nil, status.Error(codes.InvalidArgument, "request contains unsupported fields")
	}
	version, present, err := transport.ResourceVersion(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, mapError(gateways.ErrObservationRequired)
	}
	value, err := s.service.SaveIdentityProviderState(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), version, request.GetExpectedVersion(), request.GetSealedState(), request.GetCleanup())
	if err != nil {
		return nil, mapError(err)
	}
	return providerStateResponse(request.GetGatewayId(), value), nil
}
