package databasecontroller

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type failedWatchServer struct {
	pb.UnimplementedManagedDatabaseServiceServer
	code codes.Code
}

func (s failedWatchServer) WatchManagedDatabases(*pb.WatchManagedDatabasesRequest, grpc.ServerStreamingServer[pb.WatchManagedDatabasesResponse]) error {
	return status.Error(s.code, "watch ended before headers")
}

func TestDatabaseWatchPreservesErrorBeforeHeaders(t *testing.T) {
	for _, code := range []codes.Code{codes.Aborted, codes.Unavailable, codes.PermissionDenied, codes.Unauthenticated, codes.OK} {
		t.Run(code.String(), func(t *testing.T) {
			listener := bufconn.Listen(1 << 20)
			server := grpc.NewServer()
			pb.RegisterManagedDatabaseServiceServer(server, failedWatchServer{code: code})
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			defer func() { server.Stop(); listener.Close(); <-done }()
			connection, err := grpc.NewClient("passthrough:///watch", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			stream, err := pb.NewManagedDatabaseServiceClient(connection).WatchManagedDatabases(ctx, &pb.WatchManagedDatabasesRequest{})
			if err != nil {
				t.Fatal(err)
			}
			err = checkHeader(stream)
			if code == codes.OK {
				if !errors.Is(err, runtime.ErrWatch) {
					t.Fatal("missing capability was accepted", err)
				}
			} else if status.Code(err) != code || errors.Is(err, runtime.ErrWatch) {
				t.Fatal("stream error became a capability error", code, err)
			}
		})
	}
}
