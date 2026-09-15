package grpcapi

import (
	"context"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	transport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
)

func sqlStateResponse(id, cluster string, value store.EffectBinding) *pb.GatewaySQLStateBinding {
	return &pb.GatewaySQLStateBinding{GatewayId: id, ClusterId: cluster, Present: value.Present, Digest: value.Digest, Closed: value.Closed}
}
func (s *identityServer) LoadGatewaySQLState(ctx context.Context, request *pb.GatewaySQLStateRequest) (*pb.GatewaySQLStateBinding, error) {
	value, err := s.service.SQLStateBinding(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), request.GetClusterId())
	if err != nil {
		return nil, mapError(err)
	}
	return sqlStateResponse(request.GetGatewayId(), request.GetClusterId(), value), nil
}
func (s *identityServer) BindGatewaySQLState(ctx context.Context, request *pb.BindGatewaySQLStateRequest) (*pb.GatewaySQLStateBinding, error) {
	version, present, err := transport.ResourceVersion(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, mapError(gateways.ErrObservationRequired)
	}
	value, err := s.service.BindSQLState(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), request.GetClusterId(), request.GetDigest(), version)
	if err != nil {
		return nil, mapError(err)
	}
	return sqlStateResponse(request.GetGatewayId(), request.GetClusterId(), value), nil
}
func (s *identityServer) CloseGatewaySQLState(ctx context.Context, request *pb.GatewaySQLStateRequest) (*pb.GatewaySQLStateBinding, error) {
	value, err := s.service.CloseSQLState(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), request.GetClusterId())
	if err != nil {
		return nil, mapError(err)
	}
	return sqlStateResponse(request.GetGatewayId(), request.GetClusterId(), value), nil
}
