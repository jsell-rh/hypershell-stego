package gatewayworkload

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/cleanupmetrics"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayrecovery"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const QueueCapacity = 1024
const Workers = 4
const ResyncInterval = 10 * time.Second
const ReconcileTimeout = 20 * time.Second
const observationCommitTimeout = 2 * time.Second

// Provider methods can run concurrently for different Gateway IDs.
// The generated runtime serializes actions for each ID within one Run call.
type Provider interface {
	Handles(*pb.Gateway) bool
	CleanupTarget() string
	Ensure(context.Context, *pb.Gateway, *pb.ManagedDatabase, *pb.GatewayRelease) error
	Delete(context.Context, *pb.Gateway) error
	GatewayIDs(context.Context) ([]string, error)
}
type Controller struct {
	gateways  pb.GatewayServiceClient
	state     control.GatewayIdentityServiceClient
	databases pb.ManagedDatabaseServiceClient
	releases  pb.GatewayReleaseServiceClient
	provider  Provider
}

func New(gateways pb.GatewayServiceClient, state control.GatewayIdentityServiceClient, databases pb.ManagedDatabaseServiceClient, releases pb.GatewayReleaseServiceClient, provider Provider) (*Controller, error) {
	if gateways == nil || state == nil || databases == nil || releases == nil || provider == nil {
		return nil, errors.New("Gateway workload controller dependencies are required")
	}
	return &Controller{gateways, state, databases, releases, provider}, nil
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
			Cleanup:  cleanupmetrics.Gateway(c.state, "workload", c.provider.CleanupTarget()),
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
					slog.Info("Gateway workload watch started")
				case "scan_completed":
					slog.Info("Gateway workload scan completed")
				case "scan_failed":
					slog.Warn("Gateway workload scan needs another pass", "failure", rpc.FailureSummary(event.Err))
				case "reconnect":
					slog.Warn("Gateway workload watch will reconnect")
				case "reconcile_failed":
					slog.Warn("Gateway workload needs another pass", "failure", rpc.FailureSummary(event.Err))
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
	gw := state.GetGateway()
	if gw.GetMetadata().GetId() != id {
		return errors.New("Gateway state does not match its request")
	}
	if state.GetResourceVersion() < 1 || state.GetResourceGeneration() < 1 {
		return errors.New("Gateway state has no resource version")
	}
	target := c.provider.CleanupTarget()
	observations := state.GetCleanupTargets()["workload"]
	if observations == nil || target == "" {
		return errors.New("Gateway state has no workload cleanup target history")
	}
	complete, recorded := observations.GetTargets()[target]
	if state.GetDeleted() {
		if !recorded {
			return nil
		}
		failure := runtime.RunObservation(ctx, func(operation context.Context) error {
			return c.provider.Delete(operation, gw)
		}, func(commit context.Context, failure error) error {
			observed := failure == nil
			if complete == observed {
				return nil
			}
			writeContext, err := rpc.WithResourceVersion(commit, state.ResourceVersion)
			if err != nil {
				return err
			}
			_, err = c.state.ObserveGatewayCleanup(writeContext, &control.ObserveGatewayCleanupRequest{Id: id, Owner: "workload", Target: target, Complete: observed})
			return err
		}, runtime.ObservationOptions{WorkTimeout: ReconcileTimeout, CommitTimeout: observationCommitTimeout})
		if failure != nil {
			return failure
		}
		database, err := c.databases.GetManagedDatabase(ctx, &pb.GetManagedDatabaseRequest{Id: gw.GetDatabaseId()})
		if status.Code(err) == codes.NotFound {
			return nil
		}
		if err != nil {
			return err
		}
		db := database.GetManagedDatabase()
		if gw.GetDatabaseId() == "" || db.GetMetadata().GetId() != gw.GetDatabaseId() {
			return errors.New("deleted Gateway database does not match its placement")
		}
		if db.GetProvider() != gateways.ProviderDeployment {
			return nil
		}
		ns, err := gateways.DatabaseNamespace(db.Metadata.Id)
		if err != nil || db.GetNamespace() != ns {
			return errors.New("deleted Gateway database namespace does not match its ID")
		}
		_, err = c.databases.DeleteManagedDatabase(ctx, &pb.DeleteManagedDatabaseRequest{Id: db.Metadata.Id})
		if status.Code(err) == codes.NotFound {
			return nil
		}
		return err
	}
	if !c.provider.Handles(gw) {
		return nil
	}
	if !recorded {
		return errors.New("current workload target was not recorded before provider work")
	}
	return runtime.RunObservation(ctx, func(operation context.Context) error {
		database, err := c.databases.GetManagedDatabase(operation, &pb.GetManagedDatabaseRequest{Id: gw.GetDatabaseId()})
		if err != nil {
			return err
		}
		release, err := c.releases.GetGatewayRelease(operation, &pb.GetGatewayReleaseRequest{Id: gw.GetReleaseId()})
		if err != nil {
			return err
		}
		return c.provider.Ensure(operation, gw, database.GetManagedDatabase(), release.GetGatewayRelease())
	}, func(commit context.Context, observation error) error {
		phase, desired := "Running", "Healthy"
		if errors.Is(observation, ErrPending) {
			phase, desired = "Provisioning", "WorkloadNotReady"
			if gw.GetPhase() == "Running" || gw.GetPhase() == "Degraded" {
				phase = "Degraded"
			}
		} else if observation != nil {
			phase, desired = "Degraded", "WorkloadUnavailable"
		}
		if gw.GetStatus() == desired && gw.GetPhase() == phase && state.GetObservedGeneration() == state.GetResourceGeneration() {
			return nil
		}
		writeContext, err := rpc.WithResourceVersion(commit, state.ResourceVersion)
		if err != nil {
			return err
		}
		_, err = c.gateways.UpdateGateway(writeContext, &pb.UpdateGatewayRequest{Id: id, Phase: &phase, Status: &desired})
		return err
	}, runtime.ObservationOptions{WorkTimeout: ReconcileTimeout, CommitTimeout: observationCommitTimeout})
}
