// The identity controller connects Hypershell rules to generated clients.
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
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
	return runtime.Monitor(ctx, os.Getenv("HYPERSHELL_METRICS_ADDR"), controller.RunWithMetrics)
}
