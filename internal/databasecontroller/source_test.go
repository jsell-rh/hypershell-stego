package databasecontroller

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type scanAPI struct {
	*stateAPI
	event         *pb.WatchManagedDatabasesResponse
	page          *pb.ListManagedDatabasesResponse
	badCapability bool
}
type scanStream struct {
	grpc.ClientStream
	ctx           context.Context
	replay        bool
	event         *pb.WatchManagedDatabasesResponse
	badCapability bool
}

func (s *scanStream) Header() (metadata.MD, error) {
	if s.badCapability {
		return nil, nil
	}
	return metadata.Pairs(capability, "v1"), nil
}
func (s *scanStream) Recv() (*pb.WatchManagedDatabasesResponse, error) {
	if !s.replay {
		<-s.ctx.Done()
		return nil, s.ctx.Err()
	}
	if s.event == nil {
		return nil, io.EOF
	}
	event := s.event
	s.event = nil
	return event, nil
}
func (a *scanAPI) WatchManagedDatabases(ctx context.Context, _ *pb.WatchManagedDatabasesRequest, _ ...grpc.CallOption) (pb.ManagedDatabaseService_WatchManagedDatabasesClient, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	replay := len(md.Get(replayMode)) > 0
	return &scanStream{ctx: ctx, replay: replay, event: a.event, badCapability: replay && a.badCapability}, nil
}
func (a *scanAPI) ListManagedDatabases(context.Context, *pb.ListManagedDatabasesRequest, ...grpc.CallOption) (*pb.ListManagedDatabasesResponse, error) {
	return a.page, nil
}

func TestInvalidRecoveryStopsBeforeProviderWork(t *testing.T) {
	for _, tc := range []struct {
		name          string
		event         *pb.WatchManagedDatabasesResponse
		page          *pb.ListManagedDatabasesResponse
		badCapability bool
	}{
		{name: "missing replay capability", badCapability: true},
		{name: "live replay row", event: &pb.WatchManagedDatabasesResponse{Type: pb.EventType_EVENT_TYPE_CREATED}},
		{name: "missing replay resource", event: &pb.WatchManagedDatabasesResponse{Type: pb.EventType_EVENT_TYPE_DELETED, ResourceId: "database"}},
		{name: "mismatched replay ID", event: &pb.WatchManagedDatabasesResponse{Type: pb.EventType_EVENT_TYPE_DELETED, ResourceId: "database", ManagedDatabase: &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "other"}}}},
		{name: "nil page"},
		{name: "oversized page", page: &pb.ListManagedDatabasesResponse{Items: make([]*pb.ManagedDatabase, 21)}},
		{name: "nil row", page: &pb.ListManagedDatabasesResponse{Items: []*pb.ManagedDatabase{nil}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &scanAPI{stateAPI: &stateAPI{}, event: tc.event, page: tc.page, badCapability: tc.badCapability}
			provider := &recordingProvider{}
			controller, err := New(api, api, provider)
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			err = controller.Run(ctx)
			want := runtime.ErrScanContract
			if tc.badCapability {
				want = runtime.ErrWatch
			}
			if !errors.Is(err, want) || ctx.Err() != nil {
				t.Fatal("invalid recovery did not stop the runtime", err)
			}
			if api.reads != 0 || len(api.updates) != 0 || len(provider.ensured) != 0 || len(provider.deleted) != 0 {
				t.Fatal("invalid recovery reached resource work")
			}
		})
	}
}
