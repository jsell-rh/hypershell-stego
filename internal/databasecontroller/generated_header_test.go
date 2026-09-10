package databasecontroller

import (
	"context"
	"crypto/tls"
	"encoding/pem"
	"errors"
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
)

// Use the generated TLS client here. A raw client alone cannot detect changes
// to absent headers made by the generated wrapper.
func TestGeneratedClientPreservesDatabaseWatchFailure(t *testing.T) {
	for _, code := range []codes.Code{codes.Aborted, codes.Unavailable, codes.PermissionDenied, codes.Unauthenticated, codes.InvalidArgument, codes.Unimplemented, codes.OK} {
		t.Run(code.String(), func(t *testing.T) {
			fixture := httptest.NewTLSServer(nil)
			certificate := fixture.TLS.Certificates[0]
			ca := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: fixture.Certificate().Raw})
			fixture.Close()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{certificate}})))
			pb.RegisterManagedDatabaseServiceServer(server, failedWatchServer{code: code})
			done := make(chan error, 1)
			go func() { done <- server.Serve(listener) }()
			defer func() { server.Stop(); listener.Close(); <-done }()
			directory := t.TempDir()
			caFile, tokenFile := filepath.Join(directory, "ca.pem"), filepath.Join(directory, "token")
			if err := os.WriteFile(caFile, ca, 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(tokenFile, []byte("fixture-token"), 0600); err != nil {
				t.Fatal(err)
			}
			client, err := rpc.New(rpc.Options{Address: listener.Addr().String(), CAFile: caFile, TokenFile: tokenFile})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			api := pb.NewManagedDatabaseServiceClient(client)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for _, replay := range []bool{false, true} {
				stream, err := api.WatchManagedDatabases(ctx, &pb.WatchManagedDatabasesRequest{})
				if err != nil {
					t.Fatal(err)
				}
				if replay {
					err = checkReplayHeader(stream)
				} else {
					err = checkHeader(stream)
				}
				if code == codes.OK {
					if !errors.Is(err, runtime.ErrWatch) {
						t.Fatal("empty success confirmed capability", err)
					}
				} else {
					unsupported := replay && (code == codes.InvalidArgument || code == codes.Unimplemented)
					if status.Code(err) != code || errors.Is(err, runtime.ErrWatch) || errors.Is(err, runtime.ErrScanContract) != unsupported {
						t.Fatal("generated client changed the watch failure", replay, code, err)
					}
				}
			}
		})
	}
}
