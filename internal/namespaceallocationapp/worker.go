// Package namespaceallocationapp connects Hypershell state to STEGO allocation.
package namespaceallocationapp

import (
	"context"
	"os"

	"github.com/jsell-rh/hypershell-stego/internal/namespaceallocation"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// Run supplies the domain adapter. STEGO owns allocation and process behavior.
func Run(ctx context.Context, metrics *runtime.Metrics) error {
	client, err := kube.New(kube.Options{ServerURL: os.Getenv("HYPERSHELL_KUBERNETES_URL"), CAFile: os.Getenv("HYPERSHELL_KUBERNETES_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_KUBERNETES_TOKEN_FILE")})
	if err != nil {
		return err
	}
	defer client.Close()
	allocator, err := allocation.New(client, os.Getenv("HYPERSHELL_CONTROL_NAMESPACE"))
	if err != nil {
		return err
	}
	connection, err := rpc.New(rpc.Options{Address: os.Getenv("HYPERSHELL_API_GRPC_ADDR"), CAFile: os.Getenv("HYPERSHELL_API_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_API_TOKEN_FILE")})
	if err != nil {
		return err
	}
	defer connection.Close()
	controller, err := namespaceallocation.New(os.Getenv("HYPERSHELL_MANAGED_CLUSTER_ID"), allocator, pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), pb.NewManagedDatabaseServiceClient(connection))
	if err != nil {
		return err
	}
	return controller.Run(ctx, metrics)
}
