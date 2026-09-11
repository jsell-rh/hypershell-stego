// Package gatewayidentityapp connects domain providers to the generated worker.
package gatewayidentityapp

import (
	"context"
	"os"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
)

// Run supplies provider setup and the domain controller to STEGO.
func Run(ctx context.Context, metrics *runtime.Metrics) error {
	provider, err := keycloak.NewClient(keycloak.Options{ServerURL: os.Getenv("HYPERSHELL_KEYCLOAK_URL"), Realm: os.Getenv("HYPERSHELL_KEYCLOAK_REALM"), ClientID: os.Getenv("HYPERSHELL_KEYCLOAK_CLIENT_ID"), SecretFile: os.Getenv("HYPERSHELL_KEYCLOAK_SECRET_FILE"), CAFile: os.Getenv("HYPERSHELL_KEYCLOAK_CA_FILE")})
	if err != nil {
		return err
	}
	defer provider.Close()
	connection, err := rpc.New(rpc.Options{Address: os.Getenv("HYPERSHELL_API_GRPC_ADDR"), CAFile: os.Getenv("HYPERSHELL_API_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_API_TOKEN_FILE")})
	if err != nil {
		return err
	}
	defer connection.Close()
	controller, err := gatewayidentity.New(pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), provider)
	if err != nil {
		return err
	}
	return controller.RunWithMetrics(ctx, metrics)
}
