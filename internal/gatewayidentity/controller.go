// Package gatewayidentity reconciles Gateway login configuration through public
// generated transports. It does not deploy Gateway workloads.
package gatewayidentity

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

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
	gateways pb.GatewayServiceClient
	state    control.GatewayIdentityServiceClient
	provider Provider
}

func New(gateways pb.GatewayServiceClient, state control.GatewayIdentityServiceClient, provider Provider) (*Controller, error) {
	if gateways == nil || state == nil || provider == nil {
		return nil, errors.New("Gateway controller dependencies are required")
	}
	return &Controller{gateways: gateways, state: state, provider: provider}, nil
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
	group, declared := state.GetConditions()["identity"]
	if !declared || group == nil || group.GetConditions()["ClientReady"] == nil || state.ResourceGeneration < 1 {
		return runtime.ErrObservationContract
	}
	grants, declared := state.GetConditions()["identity_users"]
	if !declared || grants == nil || grants.GetConditions()["GrantsSynchronized"] == nil {
		return runtime.ErrObservationContract
	}
	var oidc string
	err = runtime.RunObservation(ctx, func(operation context.Context) error {
		var err error
		oidc, err = c.provider.EnsureGateway(operation, id, gateway.GetName())
		if err == nil && (oidc == "" || len(oidc) > 8192 || !utf8.ValidString(oidc) || strings.ContainsRune(oidc, 0)) {
			return errors.New("identity provider returned no configuration")
		}
		return err
	}, func(commit context.Context, observation error) error {
		reason, conditionStatus := "IdentityClientReady", "True"
		var configured *string
		if observation == nil {
			configured = &oidc
		} else if errors.Is(observation, context.DeadlineExceeded) {
			reason, conditionStatus = "IdentityObservationTimeout", "Unknown"
		} else {
			reason, conditionStatus = "IdentityProviderUnavailable", "Unknown"
		}
		condition := state.GetConditions()["identity"].GetConditions()["ClientReady"]
		if condition.GetCurrent() && condition.GetObservedGeneration() == state.ResourceGeneration && condition.GetStatus() == conditionStatus && condition.GetReason() == reason && (configured == nil || gateway.GetOidc() == oidc) {
			return nil
		}
		writeContext, err := rpc.WithResourceVersion(commit, state.ResourceVersion)
		if err != nil {
			return err
		}
		_, err = c.state.ObserveGatewayIdentity(writeContext, &control.ObserveGatewayIdentityRequest{Id: id, Oidc: configured, Reason: reason})
		if status.Code(err) == codes.Unimplemented {
			return runtime.ErrObservationContract
		}
		return err
	}, runtime.ObservationOptions{WorkTimeout: ReconcileTimeout, CommitTimeout: observationCommitTimeout})
	if err != nil {
		return err
	}
	return c.reconcileUsers(ctx, id)
}

// Common scan progress uses grant IDs. A failed provider operation remains
// eligible on the next full scan. Cancellation with failure retries that item.
func (c *Controller) reconcileUsers(ctx context.Context, id string) error {
	saved, err := c.state.LoadGatewayIdentityCycle(ctx, &control.LoadGatewayIdentityCheckpointRequest{GatewayId: id})
	if status.Code(err) == codes.Unimplemented {
		return runtime.ErrScanContract
	}
	if err != nil {
		return err
	}
	if saved == nil || saved.GatewayId != id || saved.ResourceGeneration < 1 || saved.ResourceVersion < 1 {
		return runtime.ErrScanContract
	}
	access := runtime.CheckpointAccess{
		Load: func(context.Context) (runtime.Checkpoint, error) {
			return runtime.Checkpoint{After: saved.Data, Version: saved.Version}, nil
		},
		Save: func(ctx context.Context, version int64, data string) error {
			value, err := c.state.SaveGatewayIdentityCycle(ctx, &control.SaveGatewayIdentityCycleRequest{GatewayId: id, ExpectedVersion: version, ResourceGeneration: saved.ResourceGeneration, ResourceVersion: saved.ResourceVersion, Data: data})
			if status.Code(err) == codes.Unimplemented {
				return runtime.ErrScanContract
			}
			if err != nil {
				return err
			}
			if value == nil || value.GatewayId != id || value.Version != version+1 || value.Data != data || value.ResourceGeneration != saved.ResourceGeneration || value.ResourceVersion < saved.ResourceVersion {
				return runtime.ErrScanContract
			}
			return nil
		},
	}
	var seen map[string]bool
	progress, err := runtime.ScanCycle(ctx, strconv.FormatInt(saved.ResourceGeneration, 10), access, func(ctx context.Context, after string, limit int) (runtime.CursorPage[string], error) {
		seen = make(map[string]bool)
		return c.userPage(ctx, id, after, limit)
	}, func(ctx context.Context, userID string) error {
		if seen[userID] {
			return nil
		}
		seen[userID] = true
		state, err := c.state.GetGatewayIdentityUser(ctx, &control.GetGatewayIdentityUserRequest{GatewayId: id, UserId: userID})
		if err == nil && (state == nil || state.GatewayId != id || state.UserId != userID || state.Issuer == "" || state.Subject == "") {
			return runtime.ErrScanContract
		}
		if err != nil {
			return err
		}
		return c.provider.ReconcileGatewayUser(ctx, id, state.Issuer, state.Subject, state.Role)
	}, func(err error) bool {
		return !errors.Is(err, runtime.ErrScanContract) && status.Code(err) != codes.PermissionDenied && status.Code(err) != codes.Unauthenticated
	}, runtime.ScanOptions{PageSize: 100, MaxPages: 100, PageTimeout: 5 * time.Second}, runtime.ObservationOptions{WorkTimeout: ReconcileTimeout, CommitTimeout: observationCommitTimeout})
	if err != nil {
		return err
	}
	if !progress.Complete {
		return errors.New("Gateway user scan has more references")
	}
	return nil
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
