package gatewayidentity

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type inventoryRetryState struct {
	control.GatewayIdentityServiceClient
	apiFailure bool
	attempts   chan time.Time
}

func (s *inventoryRetryState) ListGatewayReconcileIDs(context.Context, *control.ListGatewayReconcileIDsRequest, ...grpc.CallOption) (*control.ListGatewayReconcileIDsResponse, error) {
	if s.apiFailure {
		s.attempts <- time.Now()
		return nil, status.Error(codes.Unavailable, "inventory unavailable")
	}
	return &control.ListGatewayReconcileIDsResponse{}, nil
}

type inventoryRetryProvider struct {
	Provider
	attempts chan time.Time
	calls    atomic.Int32
}

func (p *inventoryRetryProvider) GatewayIDs(context.Context) ([]string, error) {
	p.calls.Add(1)
	p.attempts <- time.Now()
	return nil, errors.New("provider inventory unavailable")
}
func TestIdentityInventoryDelaySurvivesWatchReconnect(t *testing.T) {
	for _, apiFailure := range []bool{false, true} {
		name := "provider"
		if apiFailure {
			name = "API"
		}
		t.Run(name, func(t *testing.T) {
			attempts := make(chan time.Time, 16)
			state := &inventoryRetryState{apiFailure: apiFailure, attempts: attempts}
			provider := &inventoryRetryProvider{attempts: attempts}
			api := &retryWatchAPI{drop: make(chan struct{})}
			controller, err := New(api, state, provider)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			done := make(chan error, 1)
			go func() { done <- controller.Run(ctx) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("controller did not stop")
				}
			}()
			var third time.Time
			for i := 0; i < 3; i++ {
				select {
				case third = <-attempts:
				case <-ctx.Done():
					t.Fatal("inventory retries did not run")
				}
			}
			timer := time.NewTimer(50 * time.Millisecond)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				t.Fatal(ctx.Err())
			}
			close(api.drop)
			var fourth time.Time
			select {
			case fourth = <-attempts:
			case <-ctx.Done():
				t.Fatal("inventory retry did not resume")
			}
			if api.watches.Load() < 2 {
				t.Fatal("watch did not reconnect")
			}
			if delay := fourth.Sub(third); delay < 4*time.Second {
				t.Fatal("watch reconnect bypassed inventory retry delay", delay)
			}
			if apiFailure && provider.calls.Load() != 0 {
				t.Fatal("failed API discovery reached the provider")
			}
		})
	}
}
