package gatewayworkload

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
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

// Run uses one worker. A watch starts before the state scan. Queue overflow
// causes a reconnect and a new scan. No event is treated as an authoritative
// deletion. The worker always obtains current, privileged state from the API.
func (c *Controller) Run(ctx context.Context) error {
	for ctx.Err() == nil {
		err := c.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated {
			return errors.New("Gateway controller access was denied")
		}
		if err != nil {
			slog.Warn("Gateway workload watch will reconnect")
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
	}
	return nil
}
func (c *Controller) session(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	stream, err := c.gateways.WatchGateways(ctx, &pb.WatchGatewaysRequest{})
	if err != nil {
		return err
	}
	if _, err := stream.Header(); err != nil {
		return err
	}
	slog.Info("Gateway workload watch started")
	keys := make(chan string, QueueCapacity)
	failures := make(chan error, 2)
	workers.Go(func() {
		for {
			event, err := stream.Recv()
			if err != nil {
				failures <- err
				return
			}
			if event.GetResourceId() == "" {
				failures <- errors.New("Gateway watch returned an empty ID")
				return
			}
			select {
			case keys <- event.GetResourceId():
			case <-ctx.Done():
				return
			default:
				failures <- errors.New("Gateway watch queue exceeded its limit")
				return
			}
		}
	})
	workers.Go(func() {
		for {
			if err := c.seed(ctx, keys); err != nil {
				failures <- err
				return
			}
			slog.Info("Gateway workload scan completed")
			select {
			case <-ctx.Done():
				return
			case <-time.After(ResyncInterval):
			}
		}
	})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-failures:
			return err
		case id := <-keys:
			operation, stop := context.WithTimeout(ctx, ReconcileTimeout)
			err := c.reconcile(operation, id)
			stop()
			if status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated {
				return err
			}
			if err != nil {
				slog.Warn("Gateway workload needs another pass", "gateway_id", id, "error", err)
			}
		}
	}
}
func (c *Controller) seed(ctx context.Context, keys chan<- string) error {
	enqueue := func(id string) error {
		select {
		case keys <- id:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	// Each page is bounded. A scan can exceed the resync interval; it must
	// finish before another scan starts. A reconnect repeats retained state.
	after := ""
	for {
		operation, stop := context.WithTimeout(ctx, ReconcileTimeout)
		response, err := c.state.ListGatewayReconcileIDs(operation, &control.ListGatewayReconcileIDsRequest{AfterId: after})
		stop()
		if err != nil {
			return err
		}
		if response == nil || len(response.Ids) > 100 {
			return errors.New("invalid Gateway recovery page")
		}
		seen := make(map[string]bool, len(response.Ids))
		for _, id := range response.Ids {
			if _, err := Namespace(id); err != nil || id == after || seen[id] {
				return errors.New("Gateway recovery cursor did not advance")
			}
			if err := enqueue(id); err != nil {
				return err
			}
			seen[id] = true
			after = id
		}
		if len(response.Ids) < 100 {
			break
		}
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
	desired := "ready"
	if errors.Is(err, ErrPending) {
		desired = "provisioning"
	} else if err != nil {
		desired = "error"
	}
	if gw.GetStatus() != desired {
		_, writeErr := c.gateways.UpdateGateway(ctx, &pb.UpdateGatewayRequest{Id: id, Status: &desired})
		if writeErr != nil {
			return errors.Join(err, writeErr)
		}
	}
	return err
}
