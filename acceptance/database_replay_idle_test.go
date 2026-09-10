package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// The first real replay opens and confirms its header, then its receiver stalls.
type idleReplayClient struct {
	pb.ManagedDatabaseServiceClient
	attempts atomic.Int32
	entered  chan struct{}
	stopped  chan struct{}
}
type idleReplayStream struct {
	pb.ManagedDatabaseService_WatchManagedDatabasesClient
	ctx   context.Context
	owner *idleReplayClient
}

func (c *idleReplayClient) WatchManagedDatabases(ctx context.Context, request *pb.WatchManagedDatabasesRequest, options ...grpc.CallOption) (pb.ManagedDatabaseService_WatchManagedDatabasesClient, error) {
	stream, err := c.ManagedDatabaseServiceClient.WatchManagedDatabases(ctx, request, options...)
	if err != nil {
		return nil, err
	}
	md, _ := metadata.FromOutgoingContext(ctx)
	if len(md.Get("hypershell-managed-database-replay")) > 0 && c.attempts.Add(1) == 1 {
		return &idleReplayStream{ManagedDatabaseService_WatchManagedDatabasesClient: stream, ctx: ctx, owner: c}, nil
	}
	return stream, nil
}
func (s *idleReplayStream) Recv() (*pb.WatchManagedDatabasesResponse, error) {
	close(s.owner.entered)
	<-s.ctx.Done()
	close(s.owner.stopped)
	return nil, s.ctx.Err()
}

func TestDatabaseReplayIdleLimitRestoresCleanupAfterRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	settings = withCleanupGrants(t, settings, cleanupGrant("controller", "ManagedDatabase", "provider", ""))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	admin := token(t, key, "admin", "platform:admin")
	root := address + "/api/hypershell/v1/managed_databases"
	code, data := requestJSON(t, "POST", root, admin, []byte(`{"name":"idle-replay","provider":"deployment"}`))
	var row httpapi.ManagedDatabase
	if code != 201 || json.Unmarshal(data, &row) != nil {
		t.Fatal("create database", code, string(data))
	}
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Create", "manageddatabase.created")
	if code, _ := requestJSON(t, "DELETE", root+"/"+row.ID, admin, nil); code != 204 {
		t.Fatal("delete database", code)
	}
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Delete", "manageddatabase.deleted")
	awaitQueueEmpty(t, f)
	stop()
	stop, _, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection := grpcClient(t, grpcAddress, tlsIdentity)
	api := &idleReplayClient{ManagedDatabaseServiceClient: pb.NewManagedDatabaseServiceClient(connection), entered: make(chan struct{}), stopped: make(chan struct{})}
	provider := &retainedDatabaseProvider{deleted: make(chan *pb.ManagedDatabase, 8), ensured: make(chan struct{}, 1)}
	controller, err := databasecontroller.New(api, control.NewDatabaseCleanupServiceClient(connection), provider)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token(t, key, "controller"))), 45*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("controller did not join")
		}
	}()
	select {
	case <-api.entered:
	case <-ctx.Done():
		t.Fatal("replay did not start")
	}
	select {
	case <-api.stopped:
	case <-time.After(25 * time.Second):
		t.Fatal("idle replay prevented later recovery scans")
	}
	read, err := rpc.WithRetainedResourceRead(ctx)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for {
		var header metadata.MD
		_, err := api.GetManagedDatabase(read, &pb.GetManagedDatabaseRequest{Id: row.ID}, grpc.Header(&header))
		if err != nil {
			t.Fatal(err)
		}
		observations, err := rpc.ObservedCleanupObservations(header)
		if err != nil {
			t.Fatal(err)
		}
		if observations["provider"] {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("later replay did not complete cleanup")
		}
		time.Sleep(20 * time.Millisecond)
	}
	if api.attempts.Load() < 2 {
		t.Fatal("replay did not retry")
	}
	select {
	case <-provider.ensured:
		t.Fatal("deleted state reached provisioning")
	default:
	}
	readCatalogEvent(t, consumer, row.ID, "ManagedDatabases", "Delete", "manageddatabase.deleted")
}
