package databasecontroller

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

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
	for ctx.Err() == nil {
		err := c.session(ctx)
		if ctx.Err() != nil {
			return nil
		}
		if status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated {
			return errors.New("database controller access was denied")
		}
		if err != nil && !errors.Is(err, context.DeadlineExceeded) {
			slog.Warn("database controller will reconnect", "error", err)
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(time.Second):
		}
	}
	return nil
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

// A live subscription starts and is drained before the replay and list scans.
// Each new session repeats both scans. Failed cleanup remains in durable replay.
// A full queue closes this session, so loss cannot pass without another scan.
func (c *Controller) session(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	var workers sync.WaitGroup
	defer func() { cancel(); workers.Wait() }()
	stream, err := c.api.WatchManagedDatabases(ctx, &pb.WatchManagedDatabasesRequest{})
	if err != nil {
		return err
	}
	if err := checkHeader(stream); err != nil {
		return err
	}
	queue := make(chan *pb.WatchManagedDatabasesResponse, queueCapacity)
	failures := make(chan error, 2)
	workers.Go(func() {
		for {
			event, err := stream.Recv()
			if err != nil {
				failures <- err
				return
			}
			select {
			case queue <- event:
			case <-ctx.Done():
				return
			default:
				failures <- errors.New("database watch queue exceeded its limit")
				return
			}
		}
	})
	workers.Go(func() {
		for {
			if err := c.seed(ctx, queue); err != nil {
				failures <- err
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(resyncInterval):
			}
		}
	})
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-failures:
			return err
		case event := <-queue:
			err := c.reconcile(ctx, event)
			if status.Code(err) == codes.PermissionDenied || status.Code(err) == codes.Unauthenticated {
				return err
			}
			if err != nil && !errors.Is(err, ErrPending) {
				slog.Warn("database needs another pass", "database_id", event.GetResourceId(), "error", err)
			}
		}
	}
}
func (c *Controller) seed(ctx context.Context, queue chan<- *pb.WatchManagedDatabasesResponse) error {
	send := func(event *pb.WatchManagedDatabasesResponse) error {
		select {
		case queue <- event:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
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
