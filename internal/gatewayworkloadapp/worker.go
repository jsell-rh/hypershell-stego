// Package gatewayworkloadapp connects domain providers to the generated worker.
package gatewayworkloadapp

import (
	"context"
	"errors"
	"os"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	settings "github.com/jsell-rh/hypershell-stego/out/configuration"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	provisioner "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
)

// Run supplies provider setup and the domain controller to STEGO.
func Run(ctx context.Context, metrics *runtime.Metrics) error {
	api, err := settings.LoadControlAPI()
	if err != nil {
		return err
	}
	cluster, err := settings.LoadClusterWorker()
	if err != nil {
		return err
	}
	connection, err := rpc.New(rpc.Options{Address: api.Address, CAFile: api.CAFile, TokenFile: api.TokenFile})
	if err != nil {
		return err
	}
	defer connection.Close()
	state := control.NewGatewayIdentityServiceClient(connection)
	bindings, err := gatewayworkload.NewSQLBindings(state)
	if err != nil {
		return err
	}
	consoleBindings, err := gatewayworkload.NewConsoleSQLBindings(state)
	if err != nil {
		return err
	}
	var console *gatewayworkload.ConsoleOptions
	domain, image := os.Getenv("HYPERSHELL_GATEWAY_CONSOLE_DOMAIN"), os.Getenv("HYPERSHELL_GATEWAY_CONSOLE_IMAGE")
	pullFile := os.Getenv("HYPERSHELL_GATEWAY_CONSOLE_IMAGE_PULL_CONFIG_FILE")
	if domain != "" || image != "" || pullFile != "" {
		if domain == "" || image == "" {
			return errors.New("Gateway console requires a domain and pinned image")
		}
		credentials, err := rpc.New(rpc.Options{Address: os.Getenv("HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_ADDR"), CAFile: os.Getenv("HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_TOKEN_FILE")})
		if err != nil {
			return err
		}
		defer credentials.Close()
		console = &gatewayworkload.ConsoleOptions{Domain: domain, Image: image, ImagePullConfigFile: pullFile, Credentials: provisioner.NewGatewayConsoleCredentialServiceClient(credentials)}
	}
	provider, err := gatewayworkload.NewKubernetes(gatewayworkload.Options{Console: console, InternalCAFile: os.Getenv("HYPERSHELL_GATEWAY_INTERNAL_CA_FILE"), PublicRouter: os.Getenv("HYPERSHELL_GATEWAY_PUBLIC_ROUTER"), PublicDomain: os.Getenv("HYPERSHELL_GATEWAY_PUBLIC_DOMAIN"), PublicIssuer: os.Getenv("HYPERSHELL_GATEWAY_PUBLIC_ISSUER"), PublicCAFile: os.Getenv("HYPERSHELL_GATEWAY_PUBLIC_CA_FILE"), SQLBindings: bindings, ConsoleSQLBindings: consoleBindings, ControlNamespace: cluster.ControlNamespace, DatabaseConfigFile: os.Getenv("HYPERSHELL_GATEWAY_DATABASE_CONFIG_FILE"), SandboxRuntimeClass: cluster.SandboxRuntimeClass, ClusterID: cluster.ClusterID, ServerURL: cluster.ServerURL, CAFile: cluster.CAFile, TokenFile: cluster.TokenFile, ClusterIssuer: os.Getenv("HYPERSHELL_GATEWAY_CLUSTER_ISSUER"), Issuer: os.Getenv("HYPERSHELL_GATEWAY_OIDC_ISSUER"), TrustBundleFile: os.Getenv("HYPERSHELL_GATEWAY_TRUST_BUNDLE"), SandboxImage: os.Getenv("HYPERSHELL_GATEWAY_SANDBOX_IMAGE"), SupervisorImage: os.Getenv("HYPERSHELL_GATEWAY_SUPERVISOR_IMAGE")})
	if err != nil {
		return err
	}
	defer provider.Close()
	controller, err := gatewayworkload.New(pb.NewGatewayServiceClient(connection), state, pb.NewGatewayReleaseServiceClient(connection), provider)
	if err != nil {
		return err
	}
	return controller.RunWithMetrics(ctx, metrics)
}
