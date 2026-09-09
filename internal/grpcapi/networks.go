package grpcapi

import (
	"context"
	"math"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	events "github.com/jsell-rh/hypershell-stego/out/contracts/events"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type networkServer struct {
	pb.UnimplementedGatewayNetworkServiceServer
	resource *catalog.Resource[model.GatewayNetwork, catalog.NetworkCreate, catalog.NetworkPatch]
	source   events.Source
}

func presentGatewayNetwork(row model.GatewayNetwork) *pb.GatewayNetwork {
	return &pb.GatewayNetwork{Metadata: catalogMetadata(row.Meta, "GatewayNetwork", "/api/hypershell/v1/gateway_networks"), Name: row.Name, Topology: row.Topology, TunnelMode: row.TunnelMode, HubGatewayId: row.HubGatewayID, Status: row.Status}
}
func (s *networkServer) CreateGatewayNetwork(ctx context.Context, r *pb.CreateGatewayNetworkRequest) (*pb.CreateGatewayNetworkResponse, error) {
	row, err := s.resource.Create(ctx, gateways.PrincipalFromContext(ctx), catalog.NetworkCreate{Name: r.Name, Topology: r.Topology, TunnelMode: r.TunnelMode, HubGatewayID: r.HubGatewayId, Status: r.Status})
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.CreateGatewayNetworkResponse{GatewayNetwork: presentGatewayNetwork(row)}, nil
}
func (s *networkServer) UpdateGatewayNetwork(ctx context.Context, r *pb.UpdateGatewayNetworkRequest) (*pb.UpdateGatewayNetworkResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	row, err := s.resource.Update(ctx, gateways.PrincipalFromContext(ctx), r.Id, catalog.NetworkPatch{Name: r.Name, Topology: r.Topology, TunnelMode: r.TunnelMode, HubGatewayID: r.HubGatewayId, Status: r.Status})
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.UpdateGatewayNetworkResponse{GatewayNetwork: presentGatewayNetwork(row)}, nil
}
func (s *networkServer) GetGatewayNetwork(ctx context.Context, r *pb.GetGatewayNetworkRequest) (*pb.GetGatewayNetworkResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	row, err := s.resource.Get(ctx, gateways.PrincipalFromContext(ctx), r.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GetGatewayNetworkResponse{GatewayNetwork: presentGatewayNetwork(row)}, nil
}
func (s *networkServer) DeleteGatewayNetwork(ctx context.Context, r *pb.DeleteGatewayNetworkRequest) (*pb.DeleteGatewayNetworkResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if err := s.resource.Delete(ctx, gateways.PrincipalFromContext(ctx), r.Id); err != nil {
		return nil, mapError(err)
	}
	return &pb.DeleteGatewayNetworkResponse{}, nil
}
func (s *networkServer) ListGatewayNetworks(ctx context.Context, r *pb.ListGatewayNetworksRequest) (*pb.ListGatewayNetworksResponse, error) {
	page, size := catalogPage(r.Page, r.Size)
	result, err := s.resource.List(ctx, gateways.PrincipalFromContext(ctx), catalog.Query{Page: int(page), Size: int(size)})
	if err != nil {
		return nil, mapError(err)
	}
	rows, ok := result.Items.([]model.GatewayNetwork)
	if !ok || result.Total > math.MaxInt32 {
		return nil, status.Error(codes.Internal, "list result exceeds its contract")
	}
	response := &pb.ListGatewayNetworksResponse{Metadata: &pb.ListMeta{Page: page, Size: size, Total: int32(result.Total)}, Items: make([]*pb.GatewayNetwork, 0, len(rows))}
	for _, row := range rows {
		response.Items = append(response.Items, presentGatewayNetwork(row))
	}
	return response, nil
}
func (s *networkServer) WatchGatewayNetworks(_ *pb.WatchGatewayNetworksRequest, stream grpc.ServerStreamingServer[pb.WatchGatewayNetworksResponse]) error {
	return watchCatalog(s.resource, s.source, "gatewaynetwork", stream, func(row model.GatewayNetwork, kind pb.EventType, id string) *pb.WatchGatewayNetworksResponse {
		return &pb.WatchGatewayNetworksResponse{Type: kind, ResourceId: id, GatewayNetwork: presentGatewayNetwork(row)}
	})
}
