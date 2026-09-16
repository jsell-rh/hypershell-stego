package gatewayworkload

import (
	"context"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

type oldSQLServer struct {
	control.UnimplementedGatewayIdentityServiceServer
	calls atomic.Int32
}

func (s *oldSQLServer) LoadGatewaySQLState(context.Context, *control.GatewaySQLStateRequest) (*control.GatewaySQLStateBinding, error) {
	s.calls.Add(1)
	return nil, status.Error(codes.Internal, "old endpoint called")
}
func (s *oldSQLServer) BindGatewaySQLState(context.Context, *control.BindGatewaySQLStateRequest) (*control.GatewaySQLStateBinding, error) {
	s.calls.Add(1)
	return nil, status.Error(codes.Internal, "old endpoint called")
}
func (s *oldSQLServer) CloseGatewaySQLState(context.Context, *control.GatewaySQLStateRequest) (*control.GatewaySQLStateBinding, error) {
	s.calls.Add(1)
	return nil, status.Error(codes.Internal, "old endpoint called")
}

// The in-memory route table has only the old RPCs. No TCP or credentials are
// used. A new console caller must not fall back to a Gateway mutation.
func TestConsoleSQLStateRefusesOldServerWithoutGatewayCalls(t *testing.T) {
	listener := bufconn.Listen(65536)
	server := grpc.NewServer()
	implementation := new(oldSQLServer)
	descriptor := control.GatewayIdentityService_ServiceDesc
	descriptor.Methods = nil
	for _, method := range control.GatewayIdentityService_ServiceDesc.Methods {
		switch method.MethodName {
		case "LoadGatewaySQLState", "BindGatewaySQLState", "CloseGatewaySQLState":
			descriptor.Methods = append(descriptor.Methods, method)
		}
	}
	server.RegisterService(&descriptor, implementation)
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); listener.Close(); <-done })
	connection, err := grpc.NewClient("passthrough:///old-api", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return listener.Dial() }))
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	bindings, err := NewConsoleSQLBindings(control.NewGatewayIdentityServiceClient(connection))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, err = bindings.Load(ctx, "id", "cluster"); status.Code(err) != codes.Unimplemented {
		t.Fatal("console read reached an old method", err)
	}
	if _, err = bindings.Bind(ctx, "id", "cluster", strings.Repeat("a", 64)); status.Code(err) != codes.Unimplemented {
		t.Fatal("console registration reached an old method", err)
	}
	if _, err = bindings.Close(ctx, "id", "cluster"); status.Code(err) != codes.Unimplemented {
		t.Fatal("console closure reached an old method", err)
	}
	if _, err = bindings.Complete(ctx, "id", "cluster"); status.Code(err) != codes.Unimplemented {
		t.Fatal("console completion reached an old method", err)
	}
	if implementation.calls.Load() != 0 {
		t.Fatal("console state called a Gateway endpoint")
	}
}
