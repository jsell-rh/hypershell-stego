package grpcapi

import (
	"context"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	transport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
)

func sqlStateResponse(id, cluster string, component pb.GatewaySQLComponent, value store.EffectBinding) *pb.GatewaySQLStateBinding {
	return &pb.GatewaySQLStateBinding{GatewayId: id, ClusterId: cluster, Present: value.Present, Digest: value.Digest, Closed: value.Closed, Component: component}
}
func (s *identityServer) LoadGatewaySQLState(ctx context.Context, request *pb.GatewaySQLStateRequest) (*pb.GatewaySQLStateBinding, error) {
	return s.loadSQLState(ctx, request, pb.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_GATEWAY)
}
func (s *identityServer) LoadGatewayConsoleSQLState(ctx context.Context, request *pb.GatewaySQLStateRequest) (*pb.GatewaySQLStateBinding, error) {
	return s.loadSQLState(ctx, request, pb.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE)
}
func (s *identityServer) loadSQLState(ctx context.Context, request *pb.GatewaySQLStateRequest, component pb.GatewaySQLComponent) (*pb.GatewaySQLStateBinding, error) {
	value, err := s.service.SQLStateBinding(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), request.GetClusterId(), gateways.SQLComponent(component))
	if err != nil {
		return nil, mapError(err)
	}
	return sqlStateResponse(request.GetGatewayId(), request.GetClusterId(), component, value), nil
}
func (s *identityServer) BindGatewaySQLState(ctx context.Context, request *pb.BindGatewaySQLStateRequest) (*pb.GatewaySQLStateBinding, error) {
	return s.bindSQLState(ctx, request, pb.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_GATEWAY)
}
func (s *identityServer) BindGatewayConsoleSQLState(ctx context.Context, request *pb.BindGatewaySQLStateRequest) (*pb.GatewaySQLStateBinding, error) {
	return s.bindSQLState(ctx, request, pb.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE)
}
func (s *identityServer) bindSQLState(ctx context.Context, request *pb.BindGatewaySQLStateRequest, component pb.GatewaySQLComponent) (*pb.GatewaySQLStateBinding, error) {
	version, present, err := transport.ResourceVersion(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, mapError(gateways.ErrObservationRequired)
	}
	value, err := s.service.BindSQLState(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), request.GetClusterId(), request.GetDigest(), version, gateways.SQLComponent(component))
	if err != nil {
		return nil, mapError(err)
	}
	return sqlStateResponse(request.GetGatewayId(), request.GetClusterId(), component, value), nil
}
func (s *identityServer) CloseGatewaySQLState(ctx context.Context, request *pb.GatewaySQLStateRequest) (*pb.GatewaySQLStateBinding, error) {
	return s.closeSQLState(ctx, request, pb.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_GATEWAY)
}
func (s *identityServer) CloseGatewayConsoleSQLState(ctx context.Context, request *pb.GatewaySQLStateRequest) (*pb.GatewaySQLStateBinding, error) {
	return s.closeSQLState(ctx, request, pb.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE)
}
func (s *identityServer) closeSQLState(ctx context.Context, request *pb.GatewaySQLStateRequest, component pb.GatewaySQLComponent) (*pb.GatewaySQLStateBinding, error) {
	value, err := s.service.CloseSQLState(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), request.GetClusterId(), gateways.SQLComponent(component))
	if err != nil {
		return nil, mapError(err)
	}
	return sqlStateResponse(request.GetGatewayId(), request.GetClusterId(), component, value), nil
}

func (s *identityServer) CompleteGatewayConsoleSQLCleanup(ctx context.Context, request *pb.GatewaySQLStateRequest) (*pb.GatewaySQLStateBinding, error) {
	value, err := s.service.CompleteConsoleSQLCleanup(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), request.GetClusterId())
	if err != nil {
		return nil, mapError(err)
	}
	return sqlStateResponse(request.GetGatewayId(), request.GetClusterId(), pb.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE, value), nil
}
