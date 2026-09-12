// Package gatewayworkloadapp connects domain providers to the generated worker.
package gatewayworkloadapp

import (
	"context"
	"os"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
)

// Run supplies provider setup and the domain controller to STEGO.
func Run(ctx context.Context, metrics *runtime.Metrics) error {
	provider, err := gatewayworkload.NewKubernetes(gatewayworkload.Options{ControlNamespace: os.Getenv("HYPERSHELL_CONTROL_NAMESPACE"), CNPGDialAddress: os.Getenv("HYPERSHELL_CNPG_DIAL_ADDRESS"), SandboxRuntimeClass: os.Getenv("HYPERSHELL_GATEWAY_SANDBOX_RUNTIME_CLASS"), ClusterID: os.Getenv("HYPERSHELL_MANAGED_CLUSTER_ID"), ServerURL: os.Getenv("HYPERSHELL_KUBERNETES_URL"), CAFile: os.Getenv("HYPERSHELL_KUBERNETES_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_KUBERNETES_TOKEN_FILE"), ClusterIssuer: os.Getenv("HYPERSHELL_GATEWAY_CLUSTER_ISSUER"), Issuer: os.Getenv("HYPERSHELL_GATEWAY_OIDC_ISSUER"), TrustBundleFile: os.Getenv("HYPERSHELL_GATEWAY_TRUST_BUNDLE"), SandboxImage: os.Getenv("HYPERSHELL_GATEWAY_SANDBOX_IMAGE"), SupervisorImage: os.Getenv("HYPERSHELL_GATEWAY_SUPERVISOR_IMAGE")})
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
	return controller.RunWithMetrics(ctx, metrics)
}
