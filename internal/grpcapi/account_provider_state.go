package grpcapi

import (
	"context"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type accountProviderStateServer struct {
	pb.UnimplementedServiceAccountProviderStateServiceServer
	service *gateways.Service
}

func accountProviderStateResponse(gatewayID, accountID string, value store.ResourceState) *pb.ServiceAccountProviderState {
	return &pb.ServiceAccountProviderState{GatewayId: gatewayID, ServiceAccountId: accountID, Version: value.Version, SealedState: value.Data}
}
func (s *accountProviderStateServer) LoadServiceAccountProviderState(ctx context.Context, request *pb.LoadServiceAccountProviderStateRequest) (*pb.ServiceAccountProviderState, error) {
	if request == nil || len(request.ProtoReflect().GetUnknown()) != 0 {
		return nil, status.Error(codes.InvalidArgument, "request contains unsupported fields")
	}
	value, err := s.service.LoadAccountProviderState(ctx, gateways.PrincipalFromContext(ctx), request.GatewayId, request.ServiceAccountId, request.Cleanup)
	if err != nil {
		return nil, mapError(err)
	}
	return accountProviderStateResponse(request.GatewayId, request.ServiceAccountId, value), nil
}
func (s *accountProviderStateServer) SaveServiceAccountProviderState(ctx context.Context, request *pb.SaveServiceAccountProviderStateRequest) (*pb.ServiceAccountProviderState, error) {
	if request == nil || len(request.ProtoReflect().GetUnknown()) != 0 {
		return nil, status.Error(codes.InvalidArgument, "request contains unsupported fields")
	}
	value, err := s.service.SaveAccountProviderState(ctx, gateways.PrincipalFromContext(ctx), request.GatewayId, request.ServiceAccountId, request.Cleanup, request.ExpectedVersion, request.SealedState)
	if err != nil {
		return nil, mapError(err)
	}
	return accountProviderStateResponse(request.GatewayId, request.ServiceAccountId, value), nil
}
