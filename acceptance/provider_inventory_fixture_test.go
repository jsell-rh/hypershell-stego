package acceptance

import (
	"context"
	"errors"
	"strings"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *accountProvider) InventorySource(context.Context, string) (string, error) {
	return strings.Repeat("a", 64), nil
}
func (p *accountProvider) InventoryPage(ctx context.Context, id, version, after string, limit int) (runtime.CursorPage[string], error) {
	return runtime.CursorPage[string]{}, p.DeleteGateway(ctx, id)
}
func (p *accountProvider) PrepareInventoryCandidate(context.Context, string, string, string) (bool, error) {
	return false, errors.New("fixture has no inventory candidates")
}
func (p *boundedCleanupProvider) InventoryPage(ctx context.Context, id, version, after string, limit int) (runtime.CursorPage[string], error) {
	return runtime.CursorPage[string]{}, p.DeleteGateway(ctx, id)
}
func (p *journalInventoryProvider) InventoryPage(ctx context.Context, id, version, after string, limit int) (runtime.CursorPage[string], error) {
	return runtime.CursorPage[string]{}, p.DeleteGateway(ctx, id)
}
func (p *journalCleanupProvider) InventorySource(_ context.Context, id string) (string, error) {
	return p.client.GatewayInventorySource(id)
}
func (p *journalCleanupProvider) InventoryPage(ctx context.Context, id, version, after string, limit int) (runtime.CursorPage[string], error) {
	return p.client.GatewayInventoryPage(ctx, id, version, after, limit)
}
func (p *journalCleanupProvider) PrepareInventoryCandidate(ctx context.Context, id, version, providerID string) (bool, error) {
	return p.client.PrepareGatewayInventoryCandidate(ctx, id, version, providerID)
}

func (s *accountRPC) GetSource(ctx context.Context, r *pb.GatewayAccountInventorySourceRequest) (*pb.GatewayAccountInventorySource, error) {
	if err := provisionerCaller(ctx); err != nil {
		return nil, err
	}
	value, err := s.provider.InventorySource(ctx, r.GetGatewayId())
	if err != nil {
		return nil, status.Error(codes.Unavailable, "inventory unavailable")
	}
	return &pb.GatewayAccountInventorySource{SourceVersion: value}, nil
}
func (s *accountRPC) ReadPage(ctx context.Context, r *pb.GatewayAccountInventoryPageRequest) (*pb.GatewayAccountInventoryPage, error) {
	if err := provisionerCaller(ctx); err != nil {
		return nil, err
	}
	page, err := s.provider.InventoryPage(ctx, r.GetGatewayId(), r.GetSourceVersion(), r.GetAfter(), int(r.GetLimit()))
	if err != nil {
		return nil, status.Error(codes.Unavailable, "inventory unavailable")
	}
	value := &pb.GatewayAccountInventoryPage{More: page.More}
	for _, item := range page.Items {
		value.Candidates = append(value.Candidates, &pb.GatewayAccountInventoryCandidate{Cursor: item.Cursor, ProviderId: item.Value})
	}
	return value, nil
}
func (s *accountRPC) PrepareCandidate(ctx context.Context, r *pb.PrepareGatewayAccountCandidateRequest) (*pb.PrepareGatewayAccountCandidateResponse, error) {
	if err := provisionerCaller(ctx); err != nil {
		return nil, err
	}
	owned, err := s.provider.PrepareInventoryCandidate(ctx, r.GetGatewayId(), r.GetSourceVersion(), r.GetProviderId())
	if err != nil {
		return nil, status.Error(codes.Unavailable, "inventory unavailable")
	}
	return &pb.PrepareGatewayAccountCandidateResponse{Owned: owned}, nil
}
