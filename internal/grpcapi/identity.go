package grpcapi

import (
	"context"
	"errors"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	transport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"time"
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
	targets, err := row.CleanupTargets()
	if err != nil {
		return nil, mapError(err)
	}
	observations := make(map[string]*pb.CleanupTargetObservations, len(targets))
	for owner, values := range targets {
		observations[owner] = &pb.CleanupTargetObservations{Targets: values}
	}
	conditions, err := row.CurrentConditions()
	if err != nil {
		return nil, mapError(err)
	}
	conditionGroups := make(map[string]*pb.ResourceConditions, len(conditions))
	for owner, values := range conditions {
		group := &pb.ResourceConditions{Conditions: make(map[string]*pb.ResourceCondition, len(values))}
		for name, value := range values {
			stamp := ""
			if !value.LastTransitionTime.IsZero() {
				stamp = value.LastTransitionTime.UTC().Format(time.RFC3339Nano)
			}
			group.Conditions[name] = &pb.ResourceCondition{Status: value.Status, Reason: value.Reason, Message: value.Message, ObservedGeneration: value.ObservedGeneration, LastTransitionTime: stamp, Current: value.Current}
		}
		conditionGroups[owner] = group
	}
	return &pb.GetGatewayIdentityStateResponse{Conditions: conditionGroups, CleanupTargets: observations, Cleanup: cleanup, Gateway: gateway, Deleted: row.DeletedAt.Valid, ResourceVersion: row.ResourceVersion, ResourceGeneration: row.ResourceGeneration, ObservedGeneration: row.ObservedGeneration("workload")}, nil
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
	if err := s.service.ObserveCleanup(ctx, gateways.PrincipalFromContext(ctx), request.Id, version, request.Owner, request.Target, request.Complete); err != nil {
		return nil, mapError(err)
	}
	return &pb.ObserveGatewayCleanupResponse{}, nil
}

func (s *identityServer) ScanGatewayIdentityUsers(ctx context.Context, request *pb.ScanGatewayIdentityUsersRequest) (*pb.ScanGatewayIdentityUsersResponse, error) {
	refs, more, err := s.service.IdentityUserReferences(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), request.GetAfterGrantId(), int(request.GetPageSize()))
	if err != nil {
		return nil, mapError(err)
	}
	response := &pb.ScanGatewayIdentityUsersResponse{GatewayId: request.GetGatewayId(), AfterGrantId: request.GetAfterGrantId(), HasMore: more}
	for _, ref := range refs {
		response.References = append(response.References, &pb.GatewayIdentityUserReference{GrantId: ref.GrantID, UserId: ref.UserID})
	}
	return response, nil
}

func (s *identityServer) LoadGatewayIdentityCheckpoint(ctx context.Context, request *pb.LoadGatewayIdentityCheckpointRequest) (*pb.GatewayIdentityCheckpoint, error) {
	value, err := s.service.IdentityCheckpoint(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId())
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GatewayIdentityCheckpoint{GatewayId: request.GetGatewayId(), AfterGrantId: value.After, Version: value.Version}, nil
}
func (s *identityServer) SaveGatewayIdentityCheckpoint(ctx context.Context, request *pb.SaveGatewayIdentityCheckpointRequest) (*pb.GatewayIdentityCheckpoint, error) {
	value, err := s.service.SaveIdentityCheckpoint(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), request.GetExpectedVersion(), request.GetAfterGrantId())
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GatewayIdentityCheckpoint{GatewayId: request.GetGatewayId(), AfterGrantId: value.After, Version: value.Version}, nil
}

func (s *identityServer) ObserveGatewayIdentity(ctx context.Context, request *pb.ObserveGatewayIdentityRequest) (*pb.ObserveGatewayIdentityResponse, error) {
	version, present, err := transport.ResourceVersion(ctx)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, mapError(gateways.ErrObservationRequired)
	}
	if err := s.service.ObserveIdentity(ctx, gateways.PrincipalFromContext(ctx), request.GetId(), version, request.Oidc, request.GetReason()); err != nil {
		return nil, mapError(err)
	}
	return &pb.ObserveGatewayIdentityResponse{}, nil
}

func (s *identityServer) LoadGatewayIdentityCycle(ctx context.Context, request *pb.LoadGatewayIdentityCheckpointRequest) (*pb.GatewayIdentityCycle, error) {
	value, err := s.service.IdentityScanCycle(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId())
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GatewayIdentityCycle{GatewayId: request.GetGatewayId(), Data: value.Checkpoint.After, Version: value.Checkpoint.Version, ResourceGeneration: value.Generation}, nil
}
func (s *identityServer) SaveGatewayIdentityCycle(ctx context.Context, request *pb.SaveGatewayIdentityCycleRequest) (*pb.GatewayIdentityCycle, error) {
	value, err := s.service.SaveIdentityScanCycle(ctx, gateways.PrincipalFromContext(ctx), request.GetGatewayId(), request.GetExpectedVersion(), request.GetResourceGeneration(), request.GetData())
	if err != nil {
		return nil, mapError(err)
	}
	return &pb.GatewayIdentityCycle{GatewayId: request.GetGatewayId(), Data: value.Checkpoint.After, Version: value.Checkpoint.Version, ResourceGeneration: value.Generation}, nil
}
