package databasecontroller

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type scanAPI struct {
	*stateAPI
	events        []*pb.WatchManagedDatabasesResponse
	badCapability bool
	badScope      bool
	requestedMode string
	listCalls     int
}
type scanStream struct {
	grpc.ClientStream
	ctx           context.Context
	replay        bool
	events        []*pb.WatchManagedDatabasesResponse
	badCapability bool
	badScope      bool
}

func (s *scanStream) Header() (metadata.MD, error) {
	if s.badCapability {
		return nil, nil
	}
	header := metadata.Pairs(capability, "v1")
	if s.replay && !s.badScope {
		header.Set(replayMode, "retained-v1")
	}
	return header, nil
}
func (s *scanStream) Recv() (*pb.WatchManagedDatabasesResponse, error) {
	if !s.replay {
		<-s.ctx.Done()
		return nil, s.ctx.Err()
	}
	if len(s.events) == 0 {
		return nil, io.EOF
	}
	event := s.events[0]
	s.events = s.events[1:]
	return event, nil
}
func (a *scanAPI) WatchManagedDatabases(ctx context.Context, _ *pb.WatchManagedDatabasesRequest, _ ...grpc.CallOption) (pb.ManagedDatabaseService_WatchManagedDatabasesClient, error) {
	md, _ := metadata.FromOutgoingContext(ctx)
	values := md.Get(replayMode)
	replay := len(values) > 0
	if replay {
		a.requestedMode = values[0]
	}
	return &scanStream{ctx: ctx, replay: replay, events: a.events, badCapability: replay && a.badCapability, badScope: a.badScope}, nil
}
func (a *scanAPI) ListManagedDatabases(context.Context, *pb.ListManagedDatabasesRequest, ...grpc.CallOption) (*pb.ListManagedDatabasesResponse, error) {
	a.listCalls++
	return nil, errors.New("recovery used an offset list")
}

func TestInvalidRecoveryStopsBeforeProviderWork(t *testing.T) {
	for _, tc := range []struct {
		name          string
		events        []*pb.WatchManagedDatabasesResponse
		badCapability bool
		badScope      bool
	}{
		{name: "missing replay capability", badCapability: true},
		{name: "missing replay scope", badScope: true},
		{name: "creation event", events: []*pb.WatchManagedDatabasesResponse{{Type: pb.EventType_EVENT_TYPE_CREATED}}},
		{name: "missing replay resource", events: []*pb.WatchManagedDatabasesResponse{{Type: pb.EventType_EVENT_TYPE_DELETED, ResourceId: "database"}}},
		{name: "mismatched replay ID", events: []*pb.WatchManagedDatabasesResponse{{Type: pb.EventType_EVENT_TYPE_DELETED, ResourceId: "database", ManagedDatabase: &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: "other"}}}}},
		{name: "nil row", events: []*pb.WatchManagedDatabasesResponse{nil}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			api := &scanAPI{stateAPI: &stateAPI{}, events: tc.events, badCapability: tc.badCapability, badScope: tc.badScope}
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
			if api.reads != 0 || api.listCalls != 0 || len(api.updates) != 0 || len(provider.ensured) != 0 || len(provider.deleted) != 0 {
				t.Fatal("invalid recovery reached resource work")
			}
		})
	}
}

func TestRecoveryReadsLiveAndDeletedIDsWithoutOffsetLists(t *testing.T) {
	for _, empty := range []bool{false, true} {
		api := &scanAPI{stateAPI: &stateAPI{}}
		var expected []string
		if !empty {
			for i, kind := range []pb.EventType{pb.EventType_EVENT_TYPE_UPDATED, pb.EventType_EVENT_TYPE_DELETED} {
				id := []string{"live", "deleted"}[i]
				expected = append(expected, id)
				api.events = append(api.events, &pb.WatchManagedDatabasesResponse{Type: kind, ResourceId: id, ManagedDatabase: &pb.ManagedDatabase{Metadata: &pb.ObjectReference{Id: id}}})
			}
		}
		controller, err := New(api, api, &recordingProvider{})
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		var actual []string
		err = controller.seed(ctx, func(id string) error { actual = append(actual, id); return nil })
		cancel()
		if err != nil || !slices.Equal(actual, expected) || api.requestedMode != "retained-v1" || api.listCalls != 0 {
			t.Fatal("incomplete retained recovery", actual, err)
		}
	}
}
