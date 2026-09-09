package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
)

func main() {
	if err := run(); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}
func run() error {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	provider, err := gatewayworkload.NewKubernetes(gatewayworkload.Options{ClusterID: os.Getenv("HYPERSHELL_MANAGED_CLUSTER_ID"), ServerURL: os.Getenv("HYPERSHELL_KUBERNETES_URL"), CAFile: os.Getenv("HYPERSHELL_KUBERNETES_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_KUBERNETES_TOKEN_FILE"), ClusterIssuer: os.Getenv("HYPERSHELL_GATEWAY_CLUSTER_ISSUER"), Issuer: os.Getenv("HYPERSHELL_GATEWAY_OIDC_ISSUER"), TrustBundleFile: os.Getenv("HYPERSHELL_GATEWAY_TRUST_BUNDLE"), SandboxImage: os.Getenv("HYPERSHELL_GATEWAY_SANDBOX_IMAGE"), SupervisorImage: os.Getenv("HYPERSHELL_GATEWAY_SUPERVISOR_IMAGE")})
	if err != nil {
		return err
	}
	defer provider.Close()
	connection, err := rpc.New(rpc.Options{Address: os.Getenv("HYPERSHELL_API_GRPC_ADDR"), CAFile: os.Getenv("HYPERSHELL_API_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_API_TOKEN_FILE")})
	if err != nil {
		return err
	}
	defer connection.Close()
	controller, err := gatewayworkload.New(pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), pb.NewManagedDatabaseServiceClient(connection), pb.NewGatewayReleaseServiceClient(connection), provider)
	if err != nil {
		return err
	}
	return controller.Run(ctx)
}
