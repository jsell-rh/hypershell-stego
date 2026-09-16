package serviceaccountprovisioner

import (
	"context"
	"testing"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestInventoryRPCsDenyUnverifiedCaller(t *testing.T) {
	server, err := NewServer(nil, []string{"api-provisioner"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = server.GetSource(context.Background(), &pb.GatewayAccountInventorySourceRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal("source read bypassed caller check", err)
	}
	_, err = server.ReadPage(context.Background(), &pb.GatewayAccountInventoryPageRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal("candidate read bypassed caller check", err)
	}
	_, err = server.PrepareCandidate(context.Background(), &pb.PrepareGatewayAccountCandidateRequest{})
	if status.Code(err) != codes.PermissionDenied {
		t.Fatal("closure preparation bypassed caller check", err)
	}
}
