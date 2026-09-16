package serviceaccountprovisioner

import (
	"context"
	"errors"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	provider "github.com/jsell-rh/hypershell-stego/out/keycloak"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func inventoryError(err error) error {
	if errors.Is(err, provider.ErrClientInventoryLimit) {
		return status.Error(codes.ResourceExhausted, "provider inventory limit reached")
	}
	if errors.Is(err, runtime.ErrScanContract) {
		return status.Error(codes.FailedPrecondition, "provider inventory source changed or cursor is invalid")
	}
	return providerError(err)
}
func (s *Server) GetSource(ctx context.Context, r *pb.GatewayAccountInventorySourceRequest) (*pb.GatewayAccountInventorySource, error) {
	if err := s.available(ctx); err != nil {
		return nil, err
	}
	value, err := s.provider.GatewayInventorySource(r.GetGatewayId())
	if err != nil {
		return nil, inventoryError(err)
	}
	return &pb.GatewayAccountInventorySource{SourceVersion: value}, nil
}
func (s *Server) ReadPage(ctx context.Context, r *pb.GatewayAccountInventoryPageRequest) (*pb.GatewayAccountInventoryPage, error) {
	if err := s.available(ctx); err != nil {
		return nil, err
	}
	page, err := s.provider.GatewayInventoryPage(ctx, r.GetGatewayId(), r.GetSourceVersion(), r.GetAfter(), int(r.GetLimit()))
	if err != nil {
		return nil, inventoryError(err)
	}
	result := &pb.GatewayAccountInventoryPage{More: page.More}
	for _, item := range page.Items {
		result.Candidates = append(result.Candidates, &pb.GatewayAccountInventoryCandidate{Cursor: item.Cursor, ProviderId: item.Value})
	}
	return result, nil
}
func (s *Server) PrepareCandidate(ctx context.Context, r *pb.PrepareGatewayAccountCandidateRequest) (*pb.PrepareGatewayAccountCandidateResponse, error) {
	if err := s.available(ctx); err != nil {
		return nil, err
	}
	owned, err := s.provider.PrepareGatewayInventoryCandidate(ctx, r.GetGatewayId(), r.GetSourceVersion(), r.GetProviderId())
	if err != nil {
		return nil, inventoryError(err)
	}
	return &pb.PrepareGatewayAccountCandidateResponse{Owned: owned}, nil
}
