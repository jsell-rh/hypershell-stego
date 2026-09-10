package grpcapi

import (
	"context"
	"errors"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *databaseCleanupServer) GetDatabaseCleanupSummary(ctx context.Context, request *control.GetDatabaseCleanupSummaryRequest) (*control.CleanupSummary, error) {
	result, err := s.resource.CleanupSummary(ctx, gateways.PrincipalFromContext(ctx), request.GetOwner(), request.GetProvider())
	if err != nil {
		return nil, mapError(err)
	}
	return cleanupSummary(result, request.GetOwner(), "", request.GetProvider())
}
func (s *identityServer) GetGatewayCleanupSummary(ctx context.Context, request *control.GetGatewayCleanupSummaryRequest) (*control.CleanupSummary, error) {
	result, err := s.service.CleanupSummary(ctx, gateways.PrincipalFromContext(ctx), request.GetOwner(), request.GetTarget())
	if err != nil {
		return nil, mapError(err)
	}
	return cleanupSummary(result, request.GetOwner(), request.GetTarget(), "")
}
func cleanupSummary(row store.CleanupSummary, owner, target, provider string) (*control.CleanupSummary, error) {
	response := &control.CleanupSummary{Owner: owner, Target: target, Provider: provider, Pending: row.Pending, ObservedAt: timestamppb.New(row.ObservedAt)}
	if row.OldestPending != nil {
		response.OldestPending = timestamppb.New(*row.OldestPending)
	}
	if response.ObservedAt.CheckValid() != nil || (response.OldestPending != nil && response.OldestPending.CheckValid() != nil) {
		return nil, mapError(errors.New("invalid cleanup summary timestamp"))
	}
	return response, nil
}
