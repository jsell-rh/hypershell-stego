// Package grpcapi maps the Hypershell protobuf contract to domain operations.
package grpcapi

import (
	"context"
	"encoding/json"
	"errors"
	"math"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	events "github.com/jsell-rh/hypershell-stego/out/contracts/events"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	transport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type server struct {
	pb.UnimplementedGatewayServiceServer
	service *gateways.Service
	source  events.Source
}

func Register(registrar grpc.ServiceRegistrar, repository gateways.Repository, source events.Source) error {
	if source == nil {
		return errors.New("Gateway watch requires an event source")
	}
	options, err := gateways.OptionsFromEnvironment()
	if err != nil {
		return err
	}
	provider, closeProvider, err := serviceaccounts.ProvisionerFromEnvironment()
	if err != nil {
		return err
	}
	if err := transport.OnClose(registrar, closeProvider); err != nil {
		closeProvider()
		return err
	}
	accounts, err := serviceaccounts.New(repository, provider)
	if err != nil {
		return err
	}
	options.AccountCleaner = accounts
	service, err := gateways.New(repository, options)
	if err != nil {
		return err
	}
	registrar, err = transport.PrepareRegistrar(registrar, func(ctx context.Context) error {
		if err := service.PrepareRequest(ctx, gateways.PrincipalFromContext(ctx)); err != nil {
			return mapError(err)
		}
		return nil
	})
	if err != nil {
		return err
	}
	placement, err := catalog.New(repository, service)
	if err != nil {
		return err
	}
	pb.RegisterGatewayNetworkServiceServer(registrar, &networkServer{resource: placement.Networks, source: source})
	pb.RegisterManagedClusterServiceServer(registrar, &clusterServer{resource: placement.Clusters, source: source})
	pb.RegisterGatewayReleaseServiceServer(registrar, &releaseServer{resource: placement.Releases, source: source})
	pb.RegisterManagedDatabaseServiceServer(registrar, &databaseServer{resource: placement.Databases, source: source})
	pb.RegisterGatewayServiceServer(registrar, &server{service: service, source: source})
	pb.RegisterRoleBindingServiceServer(registrar, &grantServer{service: service, source: source})
	control.RegisterGatewayIdentityServiceServer(registrar, &identityServer{service: service})
	return nil
}

func (s *server) CreateGateway(ctx context.Context, request *pb.CreateGatewayRequest) (*pb.CreateGatewayResponse, error) {
	row, err := s.service.Create(ctx, gateways.PrincipalFromContext(ctx), gateways.CreateRequest{
		Name: request.Name, ClusterID: request.ClusterId, ReleaseID: request.ReleaseId, DatabaseID: request.DatabaseId,
		ExternalDNS: request.ExternalDns, TLSMode: request.TlsMode, ServiceType: request.ServiceType, Status: request.Status, Phase: request.Phase, Image: request.Image, SupervisorImage: request.SupervisorImage, ServerDNSNames: request.ServerDnsNames, OIDC: request.Oidc, Route: request.Route, CredentialDriver: request.CredentialDriver,
	})
	if err != nil {
		return nil, mapError(err)
	}
	gateway, err := present(row)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.CreateGatewayResponse{Gateway: gateway}, nil
}
func (s *server) GetGateway(ctx context.Context, request *pb.GetGatewayRequest) (*pb.GetGatewayResponse, error) {
	if request.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	row, err := s.service.Get(ctx, gateways.PrincipalFromContext(ctx), request.Id)
	if err != nil {
		return nil, mapError(err)
	}
	gateway, err := present(row)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GetGatewayResponse{Gateway: gateway}, nil
}
func (s *server) UpdateGateway(ctx context.Context, request *pb.UpdateGatewayRequest) (*pb.UpdateGatewayResponse, error) {
	if request.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	patch := gateways.PatchRequest{
		Name: request.Name, ClusterID: request.ClusterId, ReleaseID: request.ReleaseId, DatabaseID: request.DatabaseId,
		ExternalDNS: request.ExternalDns, TLSMode: request.TlsMode, ServiceType: request.ServiceType, Status: request.Status, Phase: request.Phase,
		Image: request.Image, SupervisorImage: request.SupervisorImage, ServerDNSNames: request.ServerDnsNames, RouteAddress: request.RouteAddress,
		OIDC: request.Oidc, Route: request.Route, CredentialDriver: request.CredentialDriver,
	}
	var row model.Gateway
	var err error
	if request.ConsoleAddress != nil {
		row, err = s.service.UpdateControlPlane(ctx, gateways.PrincipalFromContext(ctx), request.Id, patch, request.ConsoleAddress)
	} else {
		row, err = s.service.Update(ctx, gateways.PrincipalFromContext(ctx), request.Id, patch)
	}
	if err != nil {
		return nil, mapError(err)
	}
	gateway, err := present(row)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.UpdateGatewayResponse{Gateway: gateway}, nil
}
func (s *server) DeleteGateway(ctx context.Context, request *pb.DeleteGatewayRequest) (*pb.DeleteGatewayResponse, error) {
	if request.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	if err := s.service.Delete(ctx, gateways.PrincipalFromContext(ctx), request.Id); err != nil {
		return nil, mapError(err)
	}
	return &pb.DeleteGatewayResponse{}, nil
}
func (s *server) AdjustActiveSandboxCount(ctx context.Context, request *pb.AdjustActiveSandboxCountRequest) (*pb.AdjustActiveSandboxCountResponse, error) {
	count, err := s.service.AdjustActiveSandboxCount(ctx, gateways.PrincipalFromContext(ctx), request.Namespace, request.Delta)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.AdjustActiveSandboxCountResponse{ActiveSandboxCount: count}, nil
}
func (s *server) SetActiveSandboxCount(ctx context.Context, request *pb.SetActiveSandboxCountRequest) (*pb.SetActiveSandboxCountResponse, error) {
	count, err := s.service.SetActiveSandboxCount(ctx, gateways.PrincipalFromContext(ctx), request.Namespace, request.Count)
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.SetActiveSandboxCountResponse{ActiveSandboxCount: count}, nil
}
func (s *server) ListGateways(ctx context.Context, request *pb.ListGatewaysRequest) (*pb.ListGatewaysResponse, error) {
	page, size := request.Page, request.Size
	if page < 1 {
		page = 1
	}
	if size < 1 || size > 500 {
		size = 20
	}
	result, err := s.service.List(ctx, gateways.PrincipalFromContext(ctx), int(page), int(size))
	if err != nil {
		return nil, mapError(err)
	}
	rows, ok := result.Items.([]model.Gateway)
	if !ok || result.Total > math.MaxInt32 {
		return nil, status.Error(codes.Internal, "list result exceeds its contract")
	}
	response := &pb.ListGatewaysResponse{Items: make([]*pb.Gateway, 0, len(rows)), Metadata: &pb.ListMeta{Page: page, Size: size, Total: int32(result.Total)}}
	for _, row := range rows {
		gateway, err := present(row)
		if err != nil {
			return nil, mapError(err)
		}
		response.Items = append(response.Items, gateway)
	}
	return response, nil
}
func present(row model.Gateway) (*pb.Gateway, error) {
	var names []string
	if len(row.ServerDnsNames) > 0 {
		if err := json.Unmarshal(row.ServerDnsNames, &names); err != nil {
			return nil, err
		}
	}
	created, updated := timestamppb.New(row.CreatedTime), timestamppb.New(row.UpdatedTime)
	if err := created.CheckValid(); err != nil {
		return nil, err
	}
	if err := updated.CheckValid(); err != nil {
		return nil, err
	}
	return &pb.Gateway{Metadata: &pb.ObjectReference{Id: row.ID, Kind: "Gateway", Href: "/api/hypershell/v1/gateways/" + row.ID, CreatedAt: created, UpdatedAt: updated}, Name: row.Name, ClusterId: row.ClusterID, ReleaseId: row.ReleaseID, DatabaseId: row.DatabaseID, Namespace: row.Namespace,
		ExternalDns: row.ExternalDns, TlsMode: row.TlsMode, ServiceType: row.ServiceType, Status: row.Status, Phase: row.Phase, Image: row.Image, SupervisorImage: row.SupervisorImage, ServerDnsNames: names, RouteAddress: row.RouteAddress, ConsoleAddress: row.ConsoleAddress, Oidc: row.Oidc, Route: row.Route, CredentialDriver: row.CredentialDriver, ActiveSandboxCount: row.ActiveSandboxCount}, nil
}
func mapError(err error) error {
	switch {
	case errors.Is(err, gateways.ErrGrantCapacity):
		return status.Error(codes.ResourceExhausted, "grant response exceeds its resource limit")
	case errors.Is(err, gateways.ErrGatewayCleanupUnavailable):
		return status.Error(codes.Unavailable, "Gateway service-account cleanup is unavailable")
	case errors.Is(err, gateways.ErrServiceAccountsExist):
		return status.Error(codes.FailedPrecondition, "service accounts require cleanup before Gateway deletion")
	case errors.Is(err, gateways.ErrIdentity):
		return status.Error(codes.Unauthenticated, "authentication is required")
	case errors.Is(err, gateways.ErrForbidden):
		return status.Error(codes.PermissionDenied, "request is forbidden")
	case errors.Is(err, gateways.ErrCountRange):
		return status.Error(codes.OutOfRange, "sandbox count exceeds its range")
	case errors.Is(err, gateways.ErrInvalid):
		return status.Error(codes.InvalidArgument, "request is invalid")
	case errors.Is(err, storage.ErrNotFound):
		return status.Error(codes.NotFound, "resource was not found")
	case errors.Is(err, storage.ErrSerialization):
		return status.Error(codes.Aborted, "resource changed during the request; retry the operation")
	case errors.Is(err, storage.ErrConflict):
		return status.Error(codes.AlreadyExists, "resource conflicts with existing state")
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return status.FromContextError(err).Err()
	default:
		return status.Error(codes.Internal, "request failed")
	}
}
