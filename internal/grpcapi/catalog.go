package grpcapi

import (
	"context"
	"errors"
	"math"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	events "github.com/jsell-rh/hypershell-stego/out/contracts/events"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	rpctransport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
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
func catalogMetadata(m model.Meta, entity, path string) *pb.ObjectReference {
	return &pb.ObjectReference{Id: m.ID, Kind: entity, Href: path + "/" + m.ID, CreatedAt: timestamppb.New(m.CreatedTime), UpdatedAt: timestamppb.New(m.UpdatedTime)}
}
func watchCatalog[T, C, P, R any](resource *catalog.Resource[T, C, P], source events.Source, prefix string, stream grpc.ServerStreamingServer[R], present func(T, pb.EventType, string) *R) error {
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
		if err := stream.Send(present(row, kind, event.ResourceKey)); err != nil {
			return err
		}
	}
}

type clusterServer struct {
	pb.UnimplementedManagedClusterServiceServer
	resource *catalog.Resource[model.ManagedCluster, catalog.ClusterCreate, catalog.ClusterPatch]
	source   events.Source
}

func presentManagedCluster(row model.ManagedCluster) *pb.ManagedCluster {
	return &pb.ManagedCluster{Metadata: catalogMetadata(row.Meta, "ManagedCluster", "/api/hypershell/v1/managed_clusters"), Name: row.Name, Provider: row.Provider, Region: row.Region, KubeconfigSecret: row.KubeconfigSecret, Status: row.Status, ApiServerUrl: row.ApiServerUrl}
}
func (s *clusterServer) CreateManagedCluster(ctx context.Context, r *pb.CreateManagedClusterRequest) (*pb.CreateManagedClusterResponse, error) {
	row, err := s.resource.Create(ctx, gateways.PrincipalFromContext(ctx), catalog.ClusterCreate{Name: r.Name, Provider: r.Provider, Region: r.Region, KubeconfigSecret: r.KubeconfigSecret, Status: r.Status, ApiServerUrl: r.ApiServerUrl})
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.CreateManagedClusterResponse{ManagedCluster: presentManagedCluster(row)}, nil
}
func (s *clusterServer) UpdateManagedCluster(ctx context.Context, r *pb.UpdateManagedClusterRequest) (*pb.UpdateManagedClusterResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	row, err := s.resource.Update(ctx, gateways.PrincipalFromContext(ctx), r.Id, catalog.ClusterPatch{Name: r.Name, Provider: r.Provider, Region: r.Region, KubeconfigSecret: r.KubeconfigSecret, Status: r.Status, ApiServerUrl: r.ApiServerUrl})
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.UpdateManagedClusterResponse{ManagedCluster: presentManagedCluster(row)}, nil
}
func (s *clusterServer) GetManagedCluster(ctx context.Context, r *pb.GetManagedClusterRequest) (*pb.GetManagedClusterResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	row, err := s.resource.Get(ctx, gateways.PrincipalFromContext(ctx), r.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GetManagedClusterResponse{ManagedCluster: presentManagedCluster(row)}, nil
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
		response.Items = append(response.Items, presentManagedCluster(row))
	}
	return response, nil
}
func (s *clusterServer) WatchManagedClusters(_ *pb.WatchManagedClustersRequest, stream grpc.ServerStreamingServer[pb.WatchManagedClustersResponse]) error {
	return watchCatalog(s.resource, s.source, "managedcluster", stream, func(row model.ManagedCluster, kind pb.EventType, id string) *pb.WatchManagedClustersResponse {
		return &pb.WatchManagedClustersResponse{Type: kind, ResourceId: id, ManagedCluster: presentManagedCluster(row)}
	})
}

type releaseServer struct {
	pb.UnimplementedGatewayReleaseServiceServer
	resource *catalog.Resource[model.GatewayRelease, catalog.ReleaseCreate, catalog.ReleasePatch]
	source   events.Source
}

func presentGatewayRelease(row model.GatewayRelease) *pb.GatewayRelease {
	return &pb.GatewayRelease{Metadata: catalogMetadata(row.Meta, "GatewayRelease", "/api/hypershell/v1/gateway_releases"), Name: row.Name, Image: row.Image, RolloutStrategy: row.RolloutStrategy, CanaryPercent: row.CanaryPercent, CanaryDuration: row.CanaryDuration, Status: row.Status}
}
func (s *releaseServer) CreateGatewayRelease(ctx context.Context, r *pb.CreateGatewayReleaseRequest) (*pb.CreateGatewayReleaseResponse, error) {
	row, err := s.resource.Create(ctx, gateways.PrincipalFromContext(ctx), catalog.ReleaseCreate{Name: r.Name, Image: r.Image, RolloutStrategy: r.RolloutStrategy, CanaryPercent: r.CanaryPercent, CanaryDuration: r.CanaryDuration, Status: r.Status})
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.CreateGatewayReleaseResponse{GatewayRelease: presentGatewayRelease(row)}, nil
}
func (s *releaseServer) UpdateGatewayRelease(ctx context.Context, r *pb.UpdateGatewayReleaseRequest) (*pb.UpdateGatewayReleaseResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	row, err := s.resource.Update(ctx, gateways.PrincipalFromContext(ctx), r.Id, catalog.ReleasePatch{Name: r.Name, Image: r.Image, RolloutStrategy: r.RolloutStrategy, CanaryPercent: r.CanaryPercent, CanaryDuration: r.CanaryDuration, Status: r.Status})
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.UpdateGatewayReleaseResponse{GatewayRelease: presentGatewayRelease(row)}, nil
}
func (s *releaseServer) GetGatewayRelease(ctx context.Context, r *pb.GetGatewayReleaseRequest) (*pb.GetGatewayReleaseResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	row, err := s.resource.Get(ctx, gateways.PrincipalFromContext(ctx), r.Id)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GetGatewayReleaseResponse{GatewayRelease: presentGatewayRelease(row)}, nil
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
		response.Items = append(response.Items, presentGatewayRelease(row))
	}
	return response, nil
}
func (s *releaseServer) WatchGatewayReleases(_ *pb.WatchGatewayReleasesRequest, stream grpc.ServerStreamingServer[pb.WatchGatewayReleasesResponse]) error {
	return watchCatalog(s.resource, s.source, "gatewayrelease", stream, func(row model.GatewayRelease, kind pb.EventType, id string) *pb.WatchGatewayReleasesResponse {
		return &pb.WatchGatewayReleasesResponse{Type: kind, ResourceId: id, GatewayRelease: presentGatewayRelease(row)}
	})
}

type databaseServer struct {
	pb.UnimplementedManagedDatabaseServiceServer
	resource *catalog.Resource[model.ManagedDatabase, catalog.DatabaseCreate, catalog.DatabasePatch]
	source   events.Source
}

func presentManagedDatabase(row model.ManagedDatabase) *pb.ManagedDatabase {
	return &pb.ManagedDatabase{Metadata: catalogMetadata(row.Meta, "ManagedDatabase", "/api/hypershell/v1/managed_databases"), Name: row.Name, Provider: row.Provider, Namespace: row.Namespace, Region: row.Region, Engine: row.Engine, EngineVersion: row.EngineVersion, InstanceClass: row.InstanceClass, ConnectionSecret: row.ConnectionSecret, Status: row.Status}
}
func (s *databaseServer) CreateManagedDatabase(ctx context.Context, r *pb.CreateManagedDatabaseRequest) (*pb.CreateManagedDatabaseResponse, error) {
	row, err := s.resource.Create(ctx, gateways.PrincipalFromContext(ctx), catalog.DatabaseCreate{Name: r.Name, Provider: r.Provider, Region: r.Region, Engine: r.Engine, EngineVersion: r.EngineVersion, InstanceClass: r.InstanceClass, ConnectionSecret: r.ConnectionSecret, Status: r.Status})
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.CreateManagedDatabaseResponse{ManagedDatabase: presentManagedDatabase(row)}, nil
}
func (s *databaseServer) UpdateManagedDatabase(ctx context.Context, r *pb.UpdateManagedDatabaseRequest) (*pb.UpdateManagedDatabaseResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	version, present, err := rpctransport.ResourceVersion(ctx)
	if err != nil {
		return nil, err
	}
	input := catalog.DatabasePatch{Name: r.Name, Provider: r.Provider, Region: r.Region, Engine: r.Engine, EngineVersion: r.EngineVersion, InstanceClass: r.InstanceClass, ConnectionSecret: r.ConnectionSecret, Status: r.Status}
	var row model.ManagedDatabase
	if present {
		row, err = s.resource.UpdateIfVersion(ctx, gateways.PrincipalFromContext(ctx), r.Id, input, version)
	} else {
		row, err = s.resource.Update(ctx, gateways.PrincipalFromContext(ctx), r.Id, input)
	}
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.UpdateManagedDatabaseResponse{ManagedDatabase: presentManagedDatabase(row)}, nil
}
func (s *databaseServer) GetManagedDatabase(ctx context.Context, r *pb.GetManagedDatabaseRequest) (*pb.GetManagedDatabaseResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	retained, err := rpctransport.RetainedResourceRead(ctx)
	if err != nil {
		return nil, err
	}
	var row model.ManagedDatabase
	if retained {
		row, err = s.resource.GetRetained(ctx, gateways.PrincipalFromContext(ctx), r.Id)
	} else {
		row, err = s.resource.Get(ctx, gateways.PrincipalFromContext(ctx), r.Id)
	}
	if err != nil {
		return nil, mapError(err)
	}
	if err := rpctransport.SetResourceState(ctx, row.ResourceVersion, row.DeletedAt.Valid); err != nil {
		return nil, err
	}
	if retained {
		observations, err := row.CleanupObservations()
		if err != nil {
			return nil, mapError(err)
		}
		if err := rpctransport.SetCleanupObservations(ctx, observations); err != nil {
			return nil, err
		}
	}
	return &pb.GetManagedDatabaseResponse{ManagedDatabase: presentManagedDatabase(row)}, nil
}
func (s *databaseServer) DeleteManagedDatabase(ctx context.Context, r *pb.DeleteManagedDatabaseRequest) (*pb.DeleteManagedDatabaseResponse, error) {
	if r.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if err := s.resource.Delete(ctx, gateways.PrincipalFromContext(ctx), r.Id); err != nil {
		return nil, mapError(err)
	}
	return &pb.DeleteManagedDatabaseResponse{}, nil
}
func (s *databaseServer) ListManagedDatabases(ctx context.Context, r *pb.ListManagedDatabasesRequest) (*pb.ListManagedDatabasesResponse, error) {
	page, size := catalogPage(r.Page, r.Size)
	result, err := s.resource.List(ctx, gateways.PrincipalFromContext(ctx), catalog.Query{Page: int(page), Size: int(size)})
	if err != nil {
		return nil, mapError(err)
	}
	rows, ok := result.Items.([]model.ManagedDatabase)
	if !ok || result.Total > math.MaxInt32 {
		return nil, status.Error(codes.Internal, "list result exceeds its contract")
	}
	response := &pb.ListManagedDatabasesResponse{Metadata: &pb.ListMeta{Page: page, Size: size, Total: int32(result.Total)}, Items: make([]*pb.ManagedDatabase, 0, len(rows))}
	for _, row := range rows {
		response.Items = append(response.Items, presentManagedDatabase(row))
	}
	return response, nil
}
func (s *databaseServer) WatchManagedDatabases(_ *pb.WatchManagedDatabasesRequest, stream grpc.ServerStreamingServer[pb.WatchManagedDatabasesResponse]) error {
	md, _ := metadata.FromIncomingContext(stream.Context())
	if values := md.Get("hypershell-managed-database-replay"); len(values) != 0 {
		if len(values) != 1 || values[0] != "deleted-v1" {
			return status.Error(codes.InvalidArgument, "invalid database replay mode")
		}
		return s.replayDeletedDatabases(stream)
	}

	return watchCatalog(s.resource, s.source, "manageddatabase", stream, func(row model.ManagedDatabase, kind pb.EventType, id string) *pb.WatchManagedDatabasesResponse {
		return &pb.WatchManagedDatabasesResponse{Type: kind, ResourceId: id, ManagedDatabase: presentManagedDatabase(row)}
	})
}

func (s *databaseServer) replayDeletedDatabases(stream grpc.ServerStreamingServer[pb.WatchManagedDatabasesResponse]) error {
	ctx := stream.Context()
	principal := gateways.PrincipalFromContext(ctx)
	headerSent := false
	sendHeader := func() error {
		if headerSent {
			return nil
		}
		if err := stream.SendHeader(metadata.Pairs("hypershell-managed-database-delete-tombstones", "v1")); err != nil {
			return err
		}
		headerSent = true
		return nil
	}
	source := func(operation context.Context, after string, limit int) (runtime.CursorPage[model.ManagedDatabase], error) {
		var page runtime.CursorPage[model.ManagedDatabase]
		rows, more, err := s.resource.Deleted(operation, principal, after, limit)
		if err != nil {
			return page, mapError(err)
		}
		for _, row := range rows {
			page.Items = append(page.Items, runtime.CursorItem[model.ManagedDatabase]{Cursor: row.ID, Value: row})
		}
		page.More = more
		return page, nil
	}
	err := runtime.Scan(ctx, source, func(row model.ManagedDatabase) error {
		if err := sendHeader(); err != nil {
			return err
		}
		return stream.Send(&pb.WatchManagedDatabasesResponse{Type: pb.EventType_EVENT_TYPE_DELETED, ResourceId: row.ID, ManagedDatabase: presentManagedDatabase(row)})
	}, runtime.ScanOptions{PageSize: 100, MaxPages: 10000, PageTimeout: 20 * time.Second})
	if errors.Is(err, runtime.ErrScanContract) {
		return status.Error(codes.Internal, "invalid database replay page")
	}
	if err != nil {
		return err
	}
	// An empty authorized replay still confirms the capability.
	return sendHeader()
}
