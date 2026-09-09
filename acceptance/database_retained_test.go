package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Change event data at the client boundary. Provider work must use a fresh read.
type databaseHintClient struct {
	pb.ManagedDatabaseServiceClient
}
type databaseHintStream struct {
	pb.ManagedDatabaseService_WatchManagedDatabasesClient
}

func (c databaseHintClient) WatchManagedDatabases(ctx context.Context, request *pb.WatchManagedDatabasesRequest, options ...grpc.CallOption) (pb.ManagedDatabaseService_WatchManagedDatabasesClient, error) {
	stream, err := c.ManagedDatabaseServiceClient.WatchManagedDatabases(ctx, request, options...)
	if err != nil {
		return nil, err
	}
	return databaseHintStream{stream}, nil
}
func (s databaseHintStream) Recv() (*pb.WatchManagedDatabasesResponse, error) {
	event, err := s.ManagedDatabaseService_WatchManagedDatabasesClient.Recv()
	if err != nil {
		return nil, err
	}
	event = proto.Clone(event).(*pb.WatchManagedDatabasesResponse)
	if event.ManagedDatabase != nil {
		event.ManagedDatabase.Provider = "cnpg"
		event.ManagedDatabase.Namespace = "old-event-namespace"
		event.ManagedDatabase.Name = "old-event-name"
	}
	return event, nil
}

type retainedDatabaseProvider struct {
	deleted chan *pb.ManagedDatabase
	ensured chan struct{}
}

func (p *retainedDatabaseProvider) Ensure(context.Context, *pb.ManagedDatabase) error {
	select {
	case p.ensured <- struct{}{}:
	default:
	}
	return errors.New("deleted database reached Ensure")
}
func (p *retainedDatabaseProvider) Delete(_ context.Context, row *pb.ManagedDatabase) error {
	select {
	case p.deleted <- proto.Clone(row).(*pb.ManagedDatabase):
	default:
	}
	return nil
}

func TestDatabaseRetainedReadAndCleanupAfterRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	_, connection := grpcClient(t, grpcAddress, tlsIdentity)
	client := pb.NewManagedDatabaseServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	admin := token(t, key, "admin", "platform:admin")
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	controller := call(token(t, key, "controller"))
	retained := func(parent context.Context) context.Context {
		t.Helper()
		result, err := rpc.WithRetainedResourceRead(parent)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	root := address + "/api/hypershell/v1/managed_databases"
	code, data := requestJSON(t, "POST", root, admin, []byte(`{"name":"retained","provider":"deployment"}`))
	var row httpapi.ManagedDatabase
	if code != 201 || json.Unmarshal(data, &row) != nil {
		t.Fatalf("create: %d %s", code, data)
	}
	read := func(deleted bool, version int64, name string) {
		t.Helper()
		var header metadata.MD
		response, err := client.GetManagedDatabase(retained(controller), &pb.GetManagedDatabaseRequest{Id: row.ID}, grpc.Header(&header))
		if err != nil {
			t.Fatal(err)
		}
		gotRevision, gotDeleted, err := rpc.ObservedResourceState(header)
		if err != nil || gotRevision != version || gotDeleted != deleted || response.ManagedDatabase.GetMetadata().GetId() != row.ID || response.ManagedDatabase.Name != name || response.ManagedDatabase.Namespace != row.Namespace {
			t.Fatal("invalid retained state", response, gotRevision, gotDeleted, err)
		}
	}
	read(false, 1, "retained")
	for _, bearer := range []string{admin, token(t, key, "outsider")} {
		var header metadata.MD
		_, err := client.GetManagedDatabase(retained(call(bearer)), &pb.GetManagedDatabaseRequest{Id: row.ID}, grpc.Header(&header))
		if status.Code(err) != codes.PermissionDenied || len(header.Get("resource-version")) != 0 || len(header.Get("resource-deleted")) != 0 {
			t.Fatal("denied retained read exposed state", err)
		}
	}
	for _, values := range [][]string{{""}, {"true"}, {"retained-v2"}, {"retained-v1", "retained-v1"}} {
		md, _ := metadata.FromOutgoingContext(controller)
		md = md.Copy()
		md.Set("resource-read-mode", values...)
		if _, err := client.GetManagedDatabase(metadata.NewOutgoingContext(ctx, md), &pb.GetManagedDatabaseRequest{Id: row.ID}); status.Code(err) != codes.InvalidArgument {
			t.Fatal("invalid read mode accepted", err)
		}
	}
	if code, _ := requestJSON(t, "DELETE", root+"/"+row.ID, admin, nil); code != 204 {
		t.Fatal("delete", code)
	}
	if code, _ := requestJSON(t, "GET", root+"/"+row.ID, admin, nil); code != 404 {
		t.Fatal("ordinary REST read exposed retained row", code)
	}
	if _, err := client.GetManagedDatabase(controller, &pb.GetManagedDatabaseRequest{Id: row.ID}); status.Code(err) != codes.NotFound {
		t.Fatal("ordinary RPC read exposed retained row", err)
	}
	read(true, 2, "retained")
	// Change retained data after the event was inserted. Its old snapshot is not authority.
	if _, err := f.db.Exec("UPDATE managed_databases SET name='current-retained' WHERE id=$1", row.ID); err != nil {
		t.Fatal(err)
	}
	stop()
	connection.Close()
	stop, _, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	client = pb.NewManagedDatabaseServiceClient(connection)
	read(true, 3, "current-retained")
	var missingHeader metadata.MD
	if _, err := client.GetManagedDatabase(retained(controller), &pb.GetManagedDatabaseRequest{Id: "000000000000000000000000001"}, grpc.Header(&missingHeader)); status.Code(err) != codes.NotFound || len(missingHeader.Get("resource-deleted")) != 0 {
		t.Fatal("absence returned deletion evidence", err)
	}
	provider := &retainedDatabaseProvider{deleted: make(chan *pb.ManagedDatabase, 8), ensured: make(chan struct{}, 1)}
	reconciler, err := databasecontroller.New(databaseHintClient{client}, provider)
	if err != nil {
		t.Fatal(err)
	}
	runContext, stopRun := context.WithCancel(controller)
	done := make(chan error, 1)
	go func() { done <- reconciler.Run(runContext) }()
	defer func() {
		stopRun()
		select {
		case err := <-done:
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Error("controller stopped", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
		select {
		case <-provider.ensured:
			t.Error("retained deletion reached live provider work")
		default:
		}
	}()
	select {
	case deleted := <-provider.deleted:
		if deleted.GetMetadata().GetId() != row.ID || deleted.Provider != "deployment" || deleted.Namespace != row.Namespace || deleted.Name != "current-retained" {
			t.Fatal("cleanup used event data", deleted)
		}
	case <-provider.ensured:
		t.Fatal("retained deletion reached live provider work")
	case <-ctx.Done():
		t.Fatal("retained deletion did not reach provider", ctx.Err())
	}
}
