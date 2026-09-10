package gatewayidentity

import (
	"context"
	"errors"
	"io"
	"sync/atomic"
	"testing"
	"time"

	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type retryWatchAPI struct {
	pb.GatewayServiceClient
	drop    chan struct{}
	watches atomic.Int32
}

func (a *retryWatchAPI) WatchGateways(ctx context.Context, _ *pb.WatchGatewaysRequest, _ ...grpc.CallOption) (pb.GatewayService_WatchGatewaysClient, error) {
	var drop <-chan struct{}
	if a.watches.Add(1) == 1 {
		drop = a.drop
	}
	return &retryWatchStream{ctx: ctx, drop: drop}, nil
}

type retryWatchStream struct {
	pb.GatewayService_WatchGatewaysClient
	ctx  context.Context
	drop <-chan struct{}
}

func (s *retryWatchStream) Header() (metadata.MD, error) { return metadata.MD{}, nil }
func (s *retryWatchStream) Recv() (*pb.WatchGatewaysResponse, error) {
	select {
	case <-s.ctx.Done():
		return nil, s.ctx.Err()
	case <-s.drop:
		return nil, io.EOF
	}
}

type retryState struct {
	*stateFixture
	id string
}

func (s *retryState) ListGatewayReconcileIDs(context.Context, *control.ListGatewayReconcileIDsRequest, ...grpc.CallOption) (*control.ListGatewayReconcileIDsResponse, error) {
	return &control.ListGatewayReconcileIDsResponse{Ids: []string{s.id}}, nil
}

type retryProvider struct {
	Provider
	calls chan time.Time
}

func (p *retryProvider) EnsureGateway(context.Context, string, string) (string, error) {
	p.calls <- time.Now()
	return "", errors.New("provider unavailable")
}
func (p *retryProvider) GatewayIDs(context.Context) ([]string, error) { return nil, nil }

// A watch reconnect must not bypass the failed provider's growing delay.
func TestIdentityRetryDelaySurvivesWatchReconnect(t *testing.T) {
	id := ksuid.New().String()
	state := &retryState{id: id, stateFixture: &stateFixture{state: &control.GetGatewayIdentityStateResponse{
		ResourceVersion: 1, ResourceGeneration: 1, Gateway: &pb.Gateway{Metadata: &pb.ObjectReference{Id: id}, Name: "retry"},
		Conditions: map[string]*control.ResourceConditions{
			"identity":       {Conditions: map[string]*control.ResourceCondition{"ClientReady": {Status: "Unknown", Reason: "ObservationPending"}}},
			"identity_users": {Conditions: map[string]*control.ResourceCondition{"GrantsSynchronized": {Status: "Unknown", Reason: "ObservationPending"}}},
		},
	}}}
	api := &retryWatchAPI{drop: make(chan struct{})}
	provider := &retryProvider{calls: make(chan time.Time, 16)}
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
			if state.identityObservations != 1 {
				t.Error("retries repeated the unchanged failure condition", state.identityObservations)
			}
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	}()
	var third time.Time
	for i := 0; i < 3; i++ {
		select {
		case third = <-provider.calls:
		case <-ctx.Done():
			t.Fatal("provider retries did not run")
		}
	}
	// Allow the returned failure to reach the generated queue before disconnect.
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
	case fourth = <-provider.calls:
	case <-ctx.Done():
		t.Fatal("retry did not resume")
	}
	if api.watches.Load() < 2 {
		t.Fatal("watch did not reconnect")
	}
	if delay := fourth.Sub(third); delay < 4*time.Second {
		t.Fatal("watch reconnect bypassed the provider retry delay", delay)
	}
}
