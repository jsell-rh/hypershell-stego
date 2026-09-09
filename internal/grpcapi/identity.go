package grpcapi

import (
	"context"
	"errors"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	transport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	cleanup, err := row.CleanupObservations()
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GetGatewayIdentityStateResponse{Cleanup: cleanup, Gateway: gateway, Deleted: row.DeletedAt.Valid, ResourceVersion: row.ResourceVersion, ResourceGeneration: row.ResourceGeneration, ObservedGeneration: row.ObservedGeneration("workload")}, nil
}

func (s *identityServer) ListGatewayIdentityUsers(ctx context.Context, request *pb.ListGatewayIdentityUsersRequest) (*pb.ListGatewayIdentityUsersResponse, error) {
	ids, more, err := s.service.IdentityUsers(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), int(request.GetPage()))
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.ListGatewayIdentityUsersResponse{UserIds: ids, HasMore: more}, nil
}
func (s *identityServer) GetGatewayIdentityUser(ctx context.Context, request *pb.GetGatewayIdentityUserRequest) (*pb.GetGatewayIdentityUserResponse, error) {
	state, err := s.service.IdentityUserState(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), request.GetUserId())
	if errors.Is(err, gateways.ErrUnboundUser) {
		return nil, status.Error(codes.FailedPrecondition, "The stored user has no verified provider identity")
	}
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GetGatewayIdentityUserResponse{GatewayId: state.GatewayID, UserId: state.UserID, Issuer: state.Issuer, Subject: state.Subject, Role: state.Role}, nil
}

func (s *identityServer) ListGatewayReconcileIDs(ctx context.Context, request *pb.ListGatewayReconcileIDsRequest) (*pb.ListGatewayReconcileIDsResponse, error) {
	ids, err := s.service.ReconcileIDs(ctx, gateways.PrincipalFromContext(ctx), request.GetAfterId())
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.ListGatewayReconcileIDsResponse{Ids: ids}, nil
}

func (s *identityServer) SetObservedSandboxCount(ctx context.Context, request *pb.SetObservedSandboxCountRequest) (*pb.SetObservedSandboxCountResponse, error) {
	count, err := s.service.SetObservedSandboxCount(ctx, gateways.PrincipalFromContext(ctx), request.GetNamespace(), request.GetClusterId(), request.GetCount())
	if errors.Is(err, gateways.ErrPlacementChanged) {
		return nil, status.Error(codes.FailedPrecondition, "Gateway cluster assignment changed")
	}
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.SetObservedSandboxCountResponse{Count: count}, nil
}

func (s *identityServer) ObserveGatewayCleanup(ctx context.Context, request *pb.ObserveGatewayCleanupRequest) (*pb.ObserveGatewayCleanupResponse, error) {
	if request.GetId() == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}
	version, present, err := transport.ResourceVersion(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, mapError(gateways.ErrObservationRequired)
	}
	if err := s.service.ObserveCleanup(ctx, gateways.PrincipalFromContext(ctx), request.Id, version, request.Owner, request.Complete); err != nil {
		return nil, mapError(err)
	}
	return &pb.ObserveGatewayCleanupResponse{}, nil
}
