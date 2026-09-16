package serviceaccounts

import (
	"context"
	"errors"
	"testing"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type inventoryRPCFixture struct {
	pb.GatewayAccountInventoryServiceClient
	page *pb.GatewayAccountInventoryPage
	err  error
}

func (f *inventoryRPCFixture) ReadPage(context.Context, *pb.GatewayAccountInventoryPageRequest, ...grpc.CallOption) (*pb.GatewayAccountInventoryPage, error) {
	return f.page, f.err
}
func TestInventoryRPCWindowCannotBecomeEmptyCompletion(t *testing.T) {
	for _, test := range []struct {
		name      string
		page      *pb.GatewayAccountInventoryPage
		err, want error
	}{
		{"window", &pb.GatewayAccountInventoryPage{WindowLimit: true}, nil, runtime.ErrScanWindowLimit},
		{"window with items", &pb.GatewayAccountInventoryPage{WindowLimit: true, Candidates: []*pb.GatewayAccountInventoryCandidate{{Cursor: "1.1", ProviderId: "one"}}}, nil, runtime.ErrScanContract},
		{"window with continuation", &pb.GatewayAccountInventoryPage{WindowLimit: true, More: true}, nil, runtime.ErrScanContract},
		{"transport exhausted", nil, status.Error(codes.ResourceExhausted, "transport limit"), ErrUnavailable},
		{"missing response", nil, nil, runtime.ErrScanContract},
		{"empty source", &pb.GatewayAccountInventoryPage{}, nil, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &rpcProvisioner{inventory: &inventoryRPCFixture{page: test.page, err: test.err}}
			page, err := client.InventoryPage(context.Background(), "gateway", "source", "1.10000", 20)
			if !errors.Is(err, test.want) || len(page.Items) != 0 || page.More {
				t.Fatal("inventory boundary changed meaning", err)
			}
			if test.name == "transport exhausted" && errors.Is(err, runtime.ErrScanWindowLimit) {
				t.Fatal("transport error reset source")
			}
		})
	}
}
