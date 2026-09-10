package databasecontroller

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const capability = "hypershell-managed-database-delete-tombstones"
const replayMode = "hypershell-managed-database-replay"
const queueCapacity = 1024
const workers = 4
const reconcileTimeout = 20 * time.Second
const observationCommitTimeout = 2 * time.Second
const resyncInterval = 10 * time.Second

// Provider calls can run concurrently for different database IDs.
// STEGO permits one action per ID within a Run call.
type Provider interface {
	Ensure(context.Context, *pb.ManagedDatabase) error
	Delete(context.Context, *pb.ManagedDatabase) error
}
type Controller struct {
	api      pb.ManagedDatabaseServiceClient
	cleanup  control.DatabaseCleanupServiceClient
	provider Provider
}

func New(api pb.ManagedDatabaseServiceClient, cleanup control.DatabaseCleanupServiceClient, provider Provider) (*Controller, error) {
	if api == nil || cleanup == nil || provider == nil {
		return nil, errors.New("database controller dependencies are required")
	}
	return &Controller{api: api, cleanup: cleanup, provider: provider}, nil
}
func (c *Controller) Run(ctx context.Context) error {
	return c.RunWithMetrics(ctx, nil)
}

// RunWithMetrics connects optional generated diagnostics to the controller.
func (c *Controller) RunWithMetrics(ctx context.Context, metrics *runtime.Metrics) error {
	return runtime.RunKeyedWatch(ctx, runtime.Source[string]{Watch: c.watch, Scan: c.seed}, c.reconcile, runtime.KeyedWatchOptions{
		ReconnectDelay: time.Second,
		KeyedOptions: runtime.KeyedOptions{
			Metrics:  metrics,
			Capacity: queueCapacity, Workers: workers, ResyncInterval: resyncInterval,
			Timeout: reconcileTimeout, RetryMin: time.Second, RetryMax: 10 * time.Second,
			Terminal: func(err error) bool {
				return errors.Is(err, runtime.ErrObservationContract) || errors.Is(err, runtime.ErrScanContract) || errors.Is(err, runtime.ErrWatch) || status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated
			},
			Observe: func(event runtime.Event) {
				if event.Phase == "reconnect" {
					slog.Warn("database controller will reconnect")
				}
				if (event.Phase == "reconcile_failed" || event.Phase == "scan_failed") && !errors.Is(event.Err, ErrPending) {
					slog.Warn("database needs another pass", "failure", kube.FailureSummary(event.Err))
				}
			},
		},
	})
}
func (c *Controller) watch(ctx context.Context) (func() (string, error), error) {
	stream, err := c.api.WatchManagedDatabases(ctx, &pb.WatchManagedDatabasesRequest{})
	if err != nil {
		return nil, err
	}
	if err := checkHeader(stream); err != nil {
		return nil, err
	}
	return func() (string, error) {
		event, err := stream.Recv()
		if err != nil {
			return "", err
		}
		return eventKey(event)
	}, nil
}
func checkHeader(stream pb.ManagedDatabaseService_WatchManagedDatabasesClient) error {
	err := rpc.RequireStreamHeaders(stream, rpc.StreamHeader{Name: capability, Value: "v1"})
	if errors.Is(err, rpc.ErrStreamContract) {
		return fmt.Errorf("%w: database watch does not support delete tombstones", runtime.ErrWatch)
	}
	return err
}

// Hypershell selects the required capability and replay scope. STEGO checks the
// stream headers and preserves errors sent before headers.
func checkReplayHeader(stream pb.ManagedDatabaseService_WatchManagedDatabasesClient) error {
	if err := checkHeader(stream); err != nil {
		return replayOpenError(err)
	}
	err := rpc.RequireStreamHeaders(stream, rpc.StreamHeader{Name: replayMode, Value: "retained-v1"})
	if errors.Is(err, rpc.ErrStreamContract) {
		return fmt.Errorf("%w: database replay did not confirm retained rows", runtime.ErrScanContract)
	}
	return err
}

func replayOpenError(err error) error {
	if status.Code(err) == codes.InvalidArgument || status.Code(err) == codes.Unimplemented {
		return fmt.Errorf("%w: retained database replay is unavailable: %w", runtime.ErrScanContract, err)
	}
	return err
}

// Recovery reads live and deleted IDs through one finite cursor stream.
// Every row is a hint; reconciliation must read current state before work.
func (c *Controller) seed(ctx context.Context, send func(string) error) error {
	return runtime.ScanStream(ctx, func(streamContext context.Context) (func() (*pb.WatchManagedDatabasesResponse, error), error) {
		md, _ := metadata.FromOutgoingContext(streamContext)
		md = md.Copy()
		md.Set(replayMode, "retained-v1")
		replay, err := c.api.WatchManagedDatabases(metadata.NewOutgoingContext(streamContext, md), &pb.WatchManagedDatabasesRequest{})
		if err != nil {
			return nil, replayOpenError(err)
		}
		if err := checkReplayHeader(replay); err != nil {
			return nil, err
		}
		return replay.Recv, nil
	}, func(event *pb.WatchManagedDatabasesResponse) error {
		if event.GetType() != pb.EventType_EVENT_TYPE_DELETED && event.GetType() != pb.EventType_EVENT_TYPE_UPDATED {
			return fmt.Errorf("%w: invalid database replay event type", runtime.ErrScanContract)
		}
		key, err := eventKey(event)
		if err != nil {
			return err
		}
		return send(key)
	}, runtime.StreamScanOptions{MaxItems: 1000000, OpenTimeout: reconcileTimeout, ReceiveTimeout: reconcileTimeout})
}
func eventKey(event *pb.WatchManagedDatabasesResponse) (string, error) {
	db := event.GetManagedDatabase()
	if db == nil || db.GetMetadata().GetId() != event.GetResourceId() || event.GetResourceId() == "" {
		return "", fmt.Errorf("%w: database event has no matching resource", runtime.ErrScanContract)
	}
	switch event.GetType() {
	case pb.EventType_EVENT_TYPE_CREATED, pb.EventType_EVENT_TYPE_UPDATED, pb.EventType_EVENT_TYPE_DELETED:
	default:
		return "", fmt.Errorf("%w: database event type is invalid", runtime.ErrScanContract)
	}
	return event.ResourceId, nil
}

func (c *Controller) reconcile(ctx context.Context, id string) error {
	// All events are hints. Read retained state before any provider action.
	readContext, err := rpc.WithRetainedResourceRead(ctx)
	if err != nil {
		return err
	}
	var header metadata.MD
	response, err := c.api.GetManagedDatabase(readContext, &pb.GetManagedDatabaseRequest{Id: id}, grpc.Header(&header))
	if err != nil {
		return err
	}
	db := response.GetManagedDatabase()
	if db.GetMetadata().GetId() != id {
		return errors.New("database state has a different ID")
	}
	version, deleted, err := rpc.ObservedResourceState(header)
	if err != nil {
		return err
	}
	cleanup, err := rpc.ObservedCleanupObservations(header)
	if err != nil {
		return err
	}
	complete, declared := cleanup["provider"]
	if !declared {
		return errors.New("database has no declared provider cleanup owner")
	}
	if db.GetProvider() != "deployment" {
		return nil
	}
	return runtime.RunObservation(ctx, func(operation context.Context) error {
		if deleted {
			return c.provider.Delete(operation, db)
		}
		return c.provider.Ensure(operation, db)
	}, func(commit context.Context, observation error) error {
		writeContext, err := rpc.WithResourceVersion(commit, version)
		if err != nil {
			return err
		}
		if deleted {
			observed := observation == nil
			if complete == observed {
				return nil
			}
			_, err := c.cleanup.ObserveDatabaseCleanup(writeContext, &control.ObserveDatabaseCleanupRequest{Id: db.GetMetadata().GetId(), Owner: "provider", Complete: observed})
			return err
		}
		if observation != nil {
			desired := "error"
			if errors.Is(observation, ErrPending) {
				desired = "provisioning"
			}
			if db.GetStatus() == desired {
				return nil
			}
			_, err := c.api.UpdateManagedDatabase(writeContext, &pb.UpdateManagedDatabaseRequest{Id: id, Status: proto.String(desired)})
			return err
		}
		if db.GetStatus() == "ready" && db.GetConnectionSecret() == CredentialsName {
			return nil
		}
		_, err = c.api.UpdateManagedDatabase(writeContext, &pb.UpdateManagedDatabaseRequest{Id: id, Status: proto.String("ready"), ConnectionSecret: proto.String(CredentialsName)})
		return err
	}, runtime.ObservationOptions{WorkTimeout: reconcileTimeout, CommitTimeout: observationCommitTimeout})
}
