package grpcapi

import (
	"context"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	transport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type databaseCleanupServer struct {
	control.UnimplementedDatabaseCleanupServiceServer
	resource *catalog.Resource[model.ManagedDatabase, catalog.DatabaseCreate, catalog.DatabasePatch]
}

func (s *databaseCleanupServer) ObserveDatabaseCleanup(ctx context.Context, request *control.ObserveDatabaseCleanupRequest) (*control.ObserveDatabaseCleanupResponse, error) {
	if request.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	version, present, err := transport.ResourceVersion(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, mapError(gateways.ErrObservationRequired)
	}
	if err := s.resource.ObserveCleanup(ctx, gateways.PrincipalFromContext(ctx), request.Id, version, request.Owner, request.Complete); err != nil {
		return nil, mapError(err)
	}
	return &control.ObserveDatabaseCleanupResponse{}, nil
}
