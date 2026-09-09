package grpcapi

import (
	"context"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
)

type identityServer struct {
	pb.UnimplementedGatewayIdentityServiceServer
	service *gateways.Service
}

func (s *identityServer) GetGatewayIdentityState(ctx context.Context, request *pb.GetGatewayIdentityStateRequest) (*pb.GetGatewayIdentityStateResponse, error) {
	row, err := s.service.IdentityState(ctx, gateways.PrincipalFromContext(ctx), request.GetId())
	if err != nil {
		return nil, mapError(err)
	}
	gateway, err := present(row)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GetGatewayIdentityStateResponse{Gateway: gateway, Deleted: row.DeletedAt.Valid}, nil
}
