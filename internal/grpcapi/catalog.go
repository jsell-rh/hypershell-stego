package grpcapi

import (
	"context"
	"errors"
	"math"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	events "github.com/jsell-rh/hypershell-stego/out/contracts/events"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	mapping "github.com/jsell-rh/hypershell-stego/out/grpcapi/mapping"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func catalogPage(page, size int32) (int32, int32) {
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 500 {
		size = 20
	}
	return page, size
}
func watchCatalog[T, C, P, R any](resource *catalog.Resource[T, C, P], source events.Source, prefix string, stream grpc.ServerStreamingServer[R], present func(T, pb.EventType, string) (*R, error)) error {
	ctx := stream.Context()
	p := gateways.PrincipalFromContext(ctx)
	if _, err := resource.List(ctx, p, catalog.Query{Page: 1, Size: 0}); err != nil {
		return mapError(err)
	}
	sub, err := source.Subscribe(ctx)
	if err != nil {
		return watchError(err)
	}
	defer sub.Close()
	header := metadata.MD{}
	if prefix == "manageddatabase" {
		header.Set("hypershell-managed-database-delete-tombstones", "v1")
	}
	if err := stream.SendHeader(header); err != nil {
		return err
	}
	for {
		event, err := sub.Next(ctx)
		if err != nil {
			return watchError(err)
		}
		if event.Destination != "kafka" {
			continue
		}
		var kind pb.EventType
		deleted := false
		switch event.Kind {
		case prefix + ".created":
			kind = pb.EventType_EVENT_TYPE_CREATED
		case prefix + ".updated":
			kind = pb.EventType_EVENT_TYPE_UPDATED
		case prefix + ".deleted":
			kind = pb.EventType_EVENT_TYPE_DELETED
			deleted = true
		default:
			continue
		}
		row, err := resource.Event(ctx, p, event.ResourceKey, deleted)
		if errors.Is(err, storage.ErrNotFound) {
			continue
		}
		if err != nil {
			return mapError(err)
		}
		response, err := present(row, kind, event.ResourceKey)
		if err != nil {
			return mapError(err)
		}
		if err := stream.Send(response); err != nil {
			return err
		}
	}
}

type clusterServer struct {
	pb.UnimplementedManagedClusterServiceServer
	resource *catalog.Resource[model.ManagedCluster, catalog.ClusterCreate, catalog.ClusterPatch]
	source   events.Source
}

func presentManagedCluster(row model.ManagedCluster) (*pb.ManagedCluster, error) {
	return mapping.ManagedCluster(row)
}
func (s *clusterServer) CreateManagedCluster(ctx context.Context, r *pb.CreateManagedClusterRequest) (*pb.CreateManagedClusterResponse, error) {
	row, err := s.resource.Create(ctx, gateways.PrincipalFromContext(ctx), catalog.ClusterCreate{Name: r.Name, Provider: r.Provider, Region: r.Region, KubeconfigSecret: r.KubeconfigSecret, Status: r.Status, ApiServerUrl: r.ApiServerUrl})
	if err != nil {
		return nil, mapError(err)
	}
	value, err := presentManagedCluster(row)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.CreateManagedClusterResponse{ManagedCluster: value}, nil
}
func (s *clusterServer) UpdateManagedCluster(ctx context.Context, r *pb.UpdateManagedClusterRequest) (*pb.UpdateManagedClusterResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	row, err := s.resource.Update(ctx, gateways.PrincipalFromContext(ctx), r.Id, catalog.ClusterPatch{Name: r.Name, Provider: r.Provider, Region: r.Region, KubeconfigSecret: r.KubeconfigSecret, Status: r.Status, ApiServerUrl: r.ApiServerUrl})
	if err != nil {
		return nil, mapError(err)
	}
	value, err := presentManagedCluster(row)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.UpdateManagedClusterResponse{ManagedCluster: value}, nil
}
func (s *clusterServer) GetManagedCluster(ctx context.Context, r *pb.GetManagedClusterRequest) (*pb.GetManagedClusterResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	row, err := s.resource.Get(ctx, gateways.PrincipalFromContext(ctx), r.Id)
	if err != nil {
		return nil, mapError(err)
	}
	value, err := presentManagedCluster(row)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GetManagedClusterResponse{ManagedCluster: value}, nil
}
func (s *clusterServer) DeleteManagedCluster(ctx context.Context, r *pb.DeleteManagedClusterRequest) (*pb.DeleteManagedClusterResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if err := s.resource.Delete(ctx, gateways.PrincipalFromContext(ctx), r.Id); err != nil {
		return nil, mapError(err)
	}
	return &pb.DeleteManagedClusterResponse{}, nil
}
func (s *clusterServer) ListManagedClusters(ctx context.Context, r *pb.ListManagedClustersRequest) (*pb.ListManagedClustersResponse, error) {
	page, size := catalogPage(r.Page, r.Size)
	result, err := s.resource.List(ctx, gateways.PrincipalFromContext(ctx), catalog.Query{Page: int(page), Size: int(size)})
	if err != nil {
		return nil, mapError(err)
	}
	rows, ok := result.Items.([]model.ManagedCluster)
	if !ok || result.Total > math.MaxInt32 {
		return nil, status.Error(codes.Internal, "list result exceeds its contract")
	}
	response := &pb.ListManagedClustersResponse{Metadata: &pb.ListMeta{Page: page, Size: size, Total: int32(result.Total)}, Items: make([]*pb.ManagedCluster, 0, len(rows))}
	for _, row := range rows {
		value, err := presentManagedCluster(row)
		if err != nil {
			return nil, mapError(err)
		}
		response.Items = append(response.Items, value)
	}
	return response, nil
}
func (s *clusterServer) WatchManagedClusters(_ *pb.WatchManagedClustersRequest, stream grpc.ServerStreamingServer[pb.WatchManagedClustersResponse]) error {
	return watchCatalog(s.resource, s.source, "managedcluster", stream, func(row model.ManagedCluster, kind pb.EventType, id string) (*pb.WatchManagedClustersResponse, error) {
		value, err := presentManagedCluster(row)
		if err != nil {
			return nil, err
		}
		return &pb.WatchManagedClustersResponse{Type: kind, ResourceId: id, ManagedCluster: value}, nil
	})
}

type releaseServer struct {
	pb.UnimplementedGatewayReleaseServiceServer
	resource *catalog.Resource[model.GatewayRelease, catalog.ReleaseCreate, catalog.ReleasePatch]
	source   events.Source
}

func presentGatewayRelease(row model.GatewayRelease) (*pb.GatewayRelease, error) {
	return mapping.GatewayRelease(row)
}
func (s *releaseServer) CreateGatewayRelease(ctx context.Context, r *pb.CreateGatewayReleaseRequest) (*pb.CreateGatewayReleaseResponse, error) {
	row, err := s.resource.Create(ctx, gateways.PrincipalFromContext(ctx), catalog.ReleaseCreate{Name: r.Name, Image: r.Image, RolloutStrategy: r.RolloutStrategy, CanaryPercent: r.CanaryPercent, CanaryDuration: r.CanaryDuration, Status: r.Status})
	if err != nil {
		return nil, mapError(err)
	}
	value, err := presentGatewayRelease(row)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.CreateGatewayReleaseResponse{GatewayRelease: value}, nil
}
func (s *releaseServer) UpdateGatewayRelease(ctx context.Context, r *pb.UpdateGatewayReleaseRequest) (*pb.UpdateGatewayReleaseResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	row, err := s.resource.Update(ctx, gateways.PrincipalFromContext(ctx), r.Id, catalog.ReleasePatch{Name: r.Name, Image: r.Image, RolloutStrategy: r.RolloutStrategy, CanaryPercent: r.CanaryPercent, CanaryDuration: r.CanaryDuration, Status: r.Status})
	if err != nil {
		return nil, mapError(err)
	}
	value, err := presentGatewayRelease(row)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.UpdateGatewayReleaseResponse{GatewayRelease: value}, nil
}
func (s *releaseServer) GetGatewayRelease(ctx context.Context, r *pb.GetGatewayReleaseRequest) (*pb.GetGatewayReleaseResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	row, err := s.resource.Get(ctx, gateways.PrincipalFromContext(ctx), r.Id)
	if err != nil {
		return nil, mapError(err)
	}
	value, err := presentGatewayRelease(row)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GetGatewayReleaseResponse{GatewayRelease: value}, nil
}
func (s *releaseServer) DeleteGatewayRelease(ctx context.Context, r *pb.DeleteGatewayReleaseRequest) (*pb.DeleteGatewayReleaseResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if err := s.resource.Delete(ctx, gateways.PrincipalFromContext(ctx), r.Id); err != nil {
		return nil, mapError(err)
	}
	return &pb.DeleteGatewayReleaseResponse{}, nil
}
func (s *releaseServer) ListGatewayReleases(ctx context.Context, r *pb.ListGatewayReleasesRequest) (*pb.ListGatewayReleasesResponse, error) {
	page, size := catalogPage(r.Page, r.Size)
	result, err := s.resource.List(ctx, gateways.PrincipalFromContext(ctx), catalog.Query{Page: int(page), Size: int(size)})
	if err != nil {
		return nil, mapError(err)
	}
	rows, ok := result.Items.([]model.GatewayRelease)
	if !ok || result.Total > math.MaxInt32 {
		return nil, status.Error(codes.Internal, "list result exceeds its contract")
	}
	response := &pb.ListGatewayReleasesResponse{Metadata: &pb.ListMeta{Page: page, Size: size, Total: int32(result.Total)}, Items: make([]*pb.GatewayRelease, 0, len(rows))}
	for _, row := range rows {
		value, err := presentGatewayRelease(row)
		if err != nil {
			return nil, mapError(err)
		}
		response.Items = append(response.Items, value)
	}
	return response, nil
}
func (s *releaseServer) WatchGatewayReleases(_ *pb.WatchGatewayReleasesRequest, stream grpc.ServerStreamingServer[pb.WatchGatewayReleasesResponse]) error {
	return watchCatalog(s.resource, s.source, "gatewayrelease", stream, func(row model.GatewayRelease, kind pb.EventType, id string) (*pb.WatchGatewayReleasesResponse, error) {
		value, err := presentGatewayRelease(row)
		if err != nil {
			return nil, err
		}
		return &pb.WatchGatewayReleasesResponse{Type: kind, ResourceId: id, GatewayRelease: value}, nil
	})
}
