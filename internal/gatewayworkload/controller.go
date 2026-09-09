package gatewayworkload

import (
	"context"
	"errors"
	"log/slog"
	"time"

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
const ResyncInterval = 10 * time.Second
const ReconcileTimeout = 20 * time.Second

type Provider interface {
	Handles(*pb.Gateway) bool
	Owns(context.Context, *pb.Gateway) (bool, error)
	Ensure(context.Context, *pb.Gateway, *pb.ManagedDatabase, *pb.GatewayRelease) error
	Delete(context.Context, string) error
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
	return runtime.Run(ctx, runtime.Source[string]{Watch: c.watch, Scan: c.seed}, c.reconcile, runtime.Options{
		QueueCapacity: QueueCapacity, ResyncInterval: ResyncInterval,
		ReconcileTimeout: ReconcileTimeout, ReconnectDelay: time.Second,
		Terminal: func(err error) bool {
			return errors.Is(err, runtime.ErrScanContract) || status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated
		},
		Observe: func(event runtime.Event) {
			switch event.Phase {
			case "watch_started":
				slog.Info("Gateway workload watch started")
			case "scan_completed":
				slog.Info("Gateway workload scan completed")
			case "reconnect":
				slog.Warn("Gateway workload watch will reconnect")
			case "reconcile_failed":
				slog.Warn("Gateway workload needs another pass", "failure", rpc.FailureSummary(event.Err))
			}
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
	if state.GetDeleted() {
		if !c.provider.Handles(gw) {
			owned, err := c.provider.Owns(ctx, gw)
			if err != nil {
				return err
			}
			if !owned {
				return nil
			}
		}
		if err := c.provider.Delete(ctx, id); err != nil {
			return err
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
	database, err := c.databases.GetManagedDatabase(ctx, &pb.GetManagedDatabaseRequest{Id: gw.GetDatabaseId()})
	var release *pb.GetGatewayReleaseResponse
	if err == nil {
		release, err = c.releases.GetGatewayRelease(ctx, &pb.GetGatewayReleaseRequest{Id: gw.GetReleaseId()})
	}
	if err == nil {
		err = c.provider.Ensure(ctx, gw, database.GetManagedDatabase(), release.GetGatewayRelease())
	}
	phase, desired := "Running", "Healthy"
	if errors.Is(err, ErrPending) {
		phase, desired = "Provisioning", "WorkloadNotReady"
		if gw.GetPhase() == "Running" || gw.GetPhase() == "Degraded" {
			phase = "Degraded"
		}
	} else if err != nil {
		phase, desired = "Degraded", "WorkloadUnavailable"
	}
	if gw.GetStatus() != desired || gw.GetPhase() != phase {
		_, writeErr := c.gateways.UpdateGateway(ctx, &pb.UpdateGatewayRequest{Id: id, Phase: &phase, Status: &desired})
		if writeErr != nil {
			return errors.Join(err, writeErr)
		}
	}
	return err
}
