package gatewayidentity

import (
	"context"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"sync"
)

type checkpointFixture struct {
	control.GatewayIdentityServiceClient
	mu          sync.Mutex
	checkpoints map[string]runtime.Checkpoint
	cycles      map[string]runtime.Checkpoint
}

func (f *checkpointFixture) LoadGatewayIdentityCheckpoint(ctx context.Context, request *control.LoadGatewayIdentityCheckpointRequest, _ ...grpc.CallOption) (*control.GatewayIdentityCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	value := f.checkpoints[request.GatewayId]
	return &control.GatewayIdentityCheckpoint{GatewayId: request.GatewayId, Version: value.Version, AfterGrantId: value.After}, nil
}
func (f *checkpointFixture) SaveGatewayIdentityCheckpoint(ctx context.Context, request *control.SaveGatewayIdentityCheckpointRequest, _ ...grpc.CallOption) (*control.GatewayIdentityCheckpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.checkpoints == nil {
		f.checkpoints = make(map[string]runtime.Checkpoint)
	}
	value := f.checkpoints[request.GatewayId]
	if value.Version != request.ExpectedVersion {
		return nil, status.Error(codes.Aborted, "checkpoint changed")
	}
	value.Version++
	value.After = request.AfterGrantId
	f.checkpoints[request.GatewayId] = value
	return &control.GatewayIdentityCheckpoint{GatewayId: request.GatewayId, Version: value.Version, AfterGrantId: value.After}, nil
}

func (f *checkpointFixture) LoadGatewayIdentityCycle(ctx context.Context, request *control.LoadGatewayIdentityCheckpointRequest, _ ...grpc.CallOption) (*control.GatewayIdentityCycle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	value := f.cycles[request.GatewayId]
	return &control.GatewayIdentityCycle{GatewayId: request.GatewayId, Version: value.Version, Data: value.After, ResourceGeneration: 1}, nil
}
func (f *checkpointFixture) SaveGatewayIdentityCycle(ctx context.Context, request *control.SaveGatewayIdentityCycleRequest, _ ...grpc.CallOption) (*control.GatewayIdentityCycle, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cycles == nil {
		f.cycles = make(map[string]runtime.Checkpoint)
	}
	value := f.cycles[request.GatewayId]
	if value.Version != request.ExpectedVersion {
		return nil, status.Error(codes.Aborted, "checkpoint changed")
	}
	value.Version++
	value.After = request.Data
	f.cycles[request.GatewayId] = value
	return &control.GatewayIdentityCycle{GatewayId: request.GatewayId, Version: value.Version, Data: value.After, ResourceGeneration: request.ResourceGeneration}, nil
}
