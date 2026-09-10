// Package gatewayidentity reconciles Gateway login configuration through public
// generated transports. It does not deploy Gateway workloads.
package gatewayidentity

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/cleanupmetrics"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayrecovery"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const QueueCapacity = 1024
const Workers = 4
const ResyncInterval = 30 * time.Second
const ReconcileTimeout = 20 * time.Second
const observationCommitTimeout = 2 * time.Second

// Provider methods must support concurrent calls for different Gateway IDs.
// STEGO permits one action per ID within a Run call.
type Provider interface {
	EnsureGateway(context.Context, string, string) (string, error)
	DeleteGateway(context.Context, string) error
	GatewayIDs(context.Context) ([]string, error)
	ReconcileGatewayUser(context.Context, string, string, string, string) error
}
type Controller struct {
	gateways    pb.GatewayServiceClient
	state       control.GatewayIdentityServiceClient
	provider    Provider
	userScans   map[string]string
	userScansMu sync.Mutex
}

func New(gateways pb.GatewayServiceClient, state control.GatewayIdentityServiceClient, provider Provider) (*Controller, error) {
	if gateways == nil || state == nil || provider == nil {
		return nil, errors.New("Gateway controller dependencies are required")
	}
	return &Controller{gateways: gateways, state: state, provider: provider, userScans: make(map[string]string)}, nil
}

// Run connects domain state and actions to the generated controller runtime.
func (c *Controller) Run(ctx context.Context) error {
	return c.RunWithMetrics(ctx, nil)
}

// RunWithMetrics connects optional generated diagnostics to the controller.
func (c *Controller) RunWithMetrics(ctx context.Context, metrics *runtime.Metrics) error {
	return runtime.RunKeyedWatch(ctx, runtime.Source[string]{Watch: c.watch, Scan: c.seed}, c.reconcile, runtime.KeyedWatchOptions{
		ReconnectDelay: time.Second,
		KeyedOptions: runtime.KeyedOptions{
			Metrics:  metrics,
			Cleanup:  cleanupmetrics.Gateway(c.state, "identity", ""),
			Capacity: QueueCapacity, Workers: Workers, ResyncInterval: ResyncInterval,
			Timeout: ReconcileTimeout, RetryMin: time.Second, RetryMax: 10 * time.Second,
			Terminal: func(err error) bool {
				return errors.Is(err, runtime.ErrObservationContract) || errors.Is(err, runtime.ErrScanContract) || status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated
			},
			Observe: func(event runtime.Event) {
				if event.Phase == "metrics_failed" {
					slog.Warn("cleanup summary is unavailable")
				}
				switch event.Phase {
				case "watch_started":
					slog.Info("Gateway identity watch started")
				case "scan_completed":
					slog.Info("Gateway identity scan completed")
				case "scan_failed":
					slog.Warn("Gateway identity scan needs another pass", "failure", rpc.FailureSummary(event.Err))
				case "reconnect":
					slog.Warn("Gateway identity watch will reconnect")
				case "reconcile_failed":
					slog.Warn("Gateway identity needs another pass", "failure", rpc.FailureSummary(event.Err))
				}
			},
		},
	})
}
func (c *Controller) watch(ctx context.Context) (func() (string, error), error) {
	stream, err := c.gateways.WatchGateways(ctx, &pb.WatchGatewaysRequest{})
	if err != nil {
		return nil, err
	}
	if _, err := stream.Header(); err != nil {
		return nil, err
	}
	return func() (string, error) {
		event, err := stream.Recv()
		if err != nil {
			return "", err
		}
		if event.GetResourceId() == "" {
			return "", errors.New("Gateway watch returned an empty ID")
		}
		return event.GetResourceId(), nil
	}, nil
}
func (c *Controller) seed(ctx context.Context, enqueue func(string) error) error {
	if err := runtime.Scan(ctx, gatewayrecovery.Source(c.state), enqueue, runtime.ScanOptions{
		PageSize: gatewayrecovery.PageSize, MaxPages: 10000, PageTimeout: ReconcileTimeout,
	}); err != nil {
		return err
	}
	operation, stop := context.WithTimeout(ctx, ReconcileTimeout)
	ids, err := c.provider.GatewayIDs(operation)
	stop()
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := enqueue(id); err != nil {
			return err
		}
	}
	return nil
}
func (c *Controller) reconcile(ctx context.Context, id string) error {
	state, err := c.state.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: id})
	if err != nil {
		return err
	}
	gateway := state.GetGateway()
	if gateway.GetMetadata().GetId() != id {
		return errors.New("Gateway state does not match the request")
	}
	if state.GetResourceVersion() < 1 {
		return errors.New("Gateway state has no resource version")
	}
	if state.GetDeleted() {
		complete, declared := state.GetCleanup()["identity"]
		if !declared {
			return errors.New("Gateway state has no identity cleanup observation")
		}
		c.setUserScan(id, "")
		return runtime.RunObservation(ctx, func(operation context.Context) error {
			return c.provider.DeleteGateway(operation, id)
		}, func(commit context.Context, observation error) error {
			observed := observation == nil
			if complete == observed {
				return nil
			}
			writeContext, err := rpc.WithResourceVersion(commit, state.ResourceVersion)
			if err != nil {
				return err
			}
			_, err = c.state.ObserveGatewayCleanup(writeContext, &control.ObserveGatewayCleanupRequest{Id: id, Owner: "identity", Complete: observed})
			return err
		}, runtime.ObservationOptions{WorkTimeout: ReconcileTimeout, CommitTimeout: observationCommitTimeout})
	}
	var oidc string
	err = runtime.RunObservation(ctx, func(operation context.Context) error {
		var err error
		oidc, err = c.provider.EnsureGateway(operation, id, gateway.GetName())
		return err
	}, func(commit context.Context, observation error) error {
		if observation != nil || gateway.GetOidc() == oidc {
			return nil
		}
		writeContext, err := rpc.WithResourceVersion(commit, state.ResourceVersion)
		if err != nil {
			return err
		}
		_, err = c.gateways.UpdateGateway(writeContext, &pb.UpdateGatewayRequest{Id: id, Oidc: &oidc})
		return err
	}, runtime.ObservationOptions{WorkTimeout: ReconcileTimeout, CommitTimeout: observationCommitTimeout})
	if err != nil {
		return err
	}
	return c.reconcileUsers(ctx, id)
}

// The lock protects cursor data only. It is never held during a remote call.
func (c *Controller) userScan(id string) string {
	c.userScansMu.Lock()
	defer c.userScansMu.Unlock()
	return c.userScans[id]
}
func (c *Controller) setUserScan(id, after string) {
	c.userScansMu.Lock()
	defer c.userScansMu.Unlock()
	if after == "" {
		delete(c.userScans, id)
	} else {
		c.userScans[id] = after
	}
}

// Common scan progress uses grant IDs. A failed provider operation remains
// eligible on the next full scan. Cancellation with failure retries that item.
func (c *Controller) reconcileUsers(ctx context.Context, id string) error {
	var failure error
	var seen map[string]bool
	progress, err := runtime.ScanFrom(ctx, c.userScan(id), func(ctx context.Context, after string, limit int) (runtime.CursorPage[string], error) {
		seen = make(map[string]bool)
		return c.userPage(ctx, id, after, limit)
	}, func(userID string) error {
		if seen[userID] {
			return nil
		}
		seen[userID] = true
		state, err := c.state.GetGatewayIdentityUser(ctx, &control.GetGatewayIdentityUserRequest{GatewayId: id, UserId: userID})
		if err == nil && (state == nil || state.GatewayId != id || state.UserId != userID || state.Issuer == "" || state.Subject == "") {
			err = errors.New("Gateway user state does not match the request")
		}
		if err == nil {
			err = c.provider.ReconcileGatewayUser(ctx, id, state.Issuer, state.Subject, state.Role)
		}
		if err != nil && ctx.Err() != nil {
			return err
		}
		if failure == nil {
			failure = err
		}
		return nil
	}, runtime.ScanOptions{PageSize: 100, MaxPages: 100, PageTimeout: 5 * time.Second})
	if progress.Complete {
		c.setUserScan(id, "")
	} else {
		c.setUserScan(id, progress.After)
	}
	if err != nil {
		return errors.Join(failure, err)
	}
	if !progress.Complete {
		return errors.Join(failure, errors.New("Gateway user scan has more references"))
	}
	return failure
}

func (c *Controller) userPage(ctx context.Context, id, after string, limit int) (runtime.CursorPage[string], error) {
	empty := runtime.CursorPage[string]{}
	result, err := c.state.ScanGatewayIdentityUsers(ctx, &control.ScanGatewayIdentityUsersRequest{GatewayId: id, AfterGrantId: after, PageSize: int32(limit)})
	if status.Code(err) == codes.Unimplemented {
		return empty, runtime.ErrScanContract
	}
	if err != nil {
		return empty, err
	}
	if result == nil || result.GatewayId != id || result.AfterGrantId != after || len(result.References) > limit {
		return empty, runtime.ErrScanContract
	}
	page := runtime.CursorPage[string]{More: result.HasMore}
	for _, ref := range result.References {
		if ref == nil || ref.UserId == "" || len(ref.UserId) > 256 {
			return empty, runtime.ErrScanContract
		}
		page.Items = append(page.Items, runtime.CursorItem[string]{Cursor: ref.GrantId, Value: ref.UserId})
	}
	return page, nil
}
