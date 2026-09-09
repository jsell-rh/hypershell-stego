package databasecontroller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const capability = "hypershell-managed-database-delete-tombstones"
const replayMode = "hypershell-managed-database-replay"
const queueCapacity = 1024
const resyncInterval = 10 * time.Second

type Provider interface {
	Ensure(context.Context, *pb.ManagedDatabase) error
	Delete(context.Context, *pb.ManagedDatabase) error
}
type Controller struct {
	api      pb.ManagedDatabaseServiceClient
	provider Provider
}

func New(api pb.ManagedDatabaseServiceClient, provider Provider) (*Controller, error) {
	if api == nil || provider == nil {
		return nil, errors.New("database controller dependencies are required")
	}
	return &Controller{api, provider}, nil
}
func (c *Controller) Run(ctx context.Context) error {
	return runtime.Run(ctx, runtime.Source[*pb.WatchManagedDatabasesResponse]{Watch: c.watch, Scan: c.seed}, c.reconcile, runtime.Options{
		QueueCapacity: queueCapacity, ResyncInterval: resyncInterval,
		ReconcileTimeout: 20 * time.Second, ReconnectDelay: time.Second,
		Terminal: func(err error) bool {
			return status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated
		},
		Observe: func(event runtime.Event) {
			if event.Phase == "reconnect" {
				slog.Warn("database controller will reconnect")
			}
			if event.Phase == "reconcile_failed" && !errors.Is(event.Err, ErrPending) {
				slog.Warn("database needs another pass")
			}
		},
	})
}
func (c *Controller) watch(ctx context.Context) (func() (*pb.WatchManagedDatabasesResponse, error), error) {
	stream, err := c.api.WatchManagedDatabases(ctx, &pb.WatchManagedDatabasesRequest{})
	if err != nil {
		return nil, err
	}
	if err := checkHeader(stream); err != nil {
		return nil, err
	}
	return stream.Recv, nil
}
func checkHeader(stream pb.ManagedDatabaseService_WatchManagedDatabasesClient) error {
	header, err := stream.Header()
	if err != nil {
		return err
	}
	values := header.Get(capability)
	if len(values) != 1 || values[0] != "v1" {
		return errors.New("database watch does not support delete tombstones")
	}
	return nil
}

// Deleted rows use the retained replay contract. Live events remain hints.
func (c *Controller) seed(ctx context.Context, send func(*pb.WatchManagedDatabasesResponse) error) error {
	md, _ := metadata.FromOutgoingContext(ctx)
	md = md.Copy()
	md.Set(replayMode, "deleted-v1")
	replay, err := c.api.WatchManagedDatabases(metadata.NewOutgoingContext(ctx, md), &pb.WatchManagedDatabasesRequest{})
	if err != nil {
		return err
	}
	if err := checkHeader(replay); err != nil {
		return err
	}
	for {
		event, err := replay.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if event.GetType() != pb.EventType_EVENT_TYPE_DELETED {
			return errors.New("database replay returned a live row")
		}
		if err := send(event); err != nil {
			return err
		}
	}
	for page := int32(1); page <= 10000; page++ {
		response, err := c.api.ListManagedDatabases(ctx, &pb.ListManagedDatabasesRequest{Page: page, Size: 20})
		if err != nil {
			return err
		}
		if len(response.GetItems()) > 20 {
			return errors.New("database list exceeded its page size")
		}
		for _, db := range response.Items {
			if err := send(&pb.WatchManagedDatabasesResponse{Type: pb.EventType_EVENT_TYPE_UPDATED, ResourceId: db.GetMetadata().GetId(), ManagedDatabase: db}); err != nil {
				return err
			}
		}
		if len(response.Items) < 20 {
			return nil
		}
	}
	return errors.New("database scan exceeded its page limit")
}
func (c *Controller) reconcile(ctx context.Context, event *pb.WatchManagedDatabasesResponse) error {
	db := event.GetManagedDatabase()
	if db == nil || db.GetMetadata().GetId() != event.GetResourceId() || event.GetResourceId() == "" {
		return errors.New("database event has no matching resource")
	}
	if db.GetProvider() != "deployment" {
		return nil
	}
	if event.GetType() == pb.EventType_EVENT_TYPE_DELETED {
		return c.provider.Delete(ctx, db)
	}
	switch event.GetType() {
	case pb.EventType_EVENT_TYPE_CREATED, pb.EventType_EVENT_TYPE_UPDATED:
	default:
		return errors.New("database event type is invalid")
	}
	// Live events are hints. Fetch current state to avoid applying stale changes.
	response, err := c.api.GetManagedDatabase(ctx, &pb.GetManagedDatabaseRequest{Id: event.ResourceId})
	if status.Code(err) == codes.NotFound {
		return nil
	}
	if err != nil {
		return err
	}
	db = response.GetManagedDatabase()
	if db.GetMetadata().GetId() != event.ResourceId {
		return errors.New("database state has a different ID")
	}
	if err := c.provider.Ensure(ctx, db); err != nil {
		desired := "error"
		if errors.Is(err, ErrPending) {
			desired = "provisioning"
		}
		if db.GetStatus() != desired {
			_, updateError := c.api.UpdateManagedDatabase(ctx, &pb.UpdateManagedDatabaseRequest{Id: event.ResourceId, Status: proto.String(desired)})
			if updateError != nil {
				return updateError
			}
		}
		return err
	}
	if db.GetStatus() == "ready" && db.GetConnectionSecret() == CredentialsName {
		return nil
	}
	_, err = c.api.UpdateManagedDatabase(ctx, &pb.UpdateManagedDatabaseRequest{Id: event.ResourceId, Status: proto.String("ready"), ConnectionSecret: proto.String(CredentialsName)})
	return err
}
