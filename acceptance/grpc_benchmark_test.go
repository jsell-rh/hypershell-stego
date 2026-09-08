package acceptance

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/metadata"
)

func BenchmarkGRPCFilteredPage(b *testing.B) {
	f := database(b)
	for i := range 200 {
		owner := "alice"
		if i%2 == 1 {
			owner = "bob"
		}
		if _, err := f.service.Create(context.Background(), principal(owner, "gateway:creator"), f.request(fmt.Sprintf("gateway-%d", i))); err != nil {
			b.Fatal(err)
		}
	}
	_, config := broker(b, identity(b, "localhost"))
	key, settings := issuer(b)
	tlsIdentity := identity(b, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	stop, _, address := startBoth(b, buildApplication(b), f.dsn, config, settings...)
	defer stop()
	awaitQueueEmpty(b, f)
	client, _ := grpcClient(b, address, tlsIdentity)
	ctx := metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token(b, key, "alice")))
	request := &pb.ListGatewaysRequest{Page: 1, Size: 20}
	// Establish TLS before measuring repeated requests on the same connection.
	warmup, cancel := context.WithTimeout(ctx, 5*time.Second)
	if _, err := client.ListGateways(warmup, request); err != nil {
		b.Fatal(err)
	}
	cancel()
	b.ResetTimer()
	for range b.N {
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		result, err := client.ListGateways(call, request)
		cancel()
		if err != nil || result.Metadata.Total != 100 || len(result.Items) != 20 {
			b.Fatalf("filtered gRPC page: %v %v", result, err)
		}
	}
	b.StopTimer()
}
