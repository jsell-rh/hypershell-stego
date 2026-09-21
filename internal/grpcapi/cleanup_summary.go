package grpcapi

import (
	"context"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	transport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
)

func (s *identityServer) GetGatewayCleanupSummary(ctx context.Context, request *control.GetGatewayCleanupSummaryRequest) (*control.CleanupSummary, error) {
	result, err := s.service.CleanupSummary(ctx, gateways.PrincipalFromContext(ctx), request.GetOwner(), request.GetTarget())
	if err != nil {
		return nil, mapError(err)
	}
	return cleanupSummary(result, request.GetOwner(), request.GetTarget(), "")
}
func cleanupSummary(row store.CleanupSummary, owner, target, provider string) (*control.CleanupSummary, error) {
	observed, err := transport.Timestamp(row.ObservedAt)
	if err != nil {
		return nil, mapError(err)
	}
	oldest, err := transport.OptionalTimestamp(row.OldestPending)
	if err != nil {
		return nil, mapError(err)
	}
	return &control.CleanupSummary{Owner: owner, Target: target, Provider: provider, Pending: row.Pending, ObservedAt: observed, OldestPending: oldest}, nil
}
