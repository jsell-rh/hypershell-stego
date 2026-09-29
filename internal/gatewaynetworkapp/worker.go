// Package gatewaynetworkapp connects the network reconciler to the generated worker.
package gatewaynetworkapp

import (
	"context"

	"github.com/jsell-rh/hypershell-stego/internal/gatewaynetwork"
	settings "github.com/jsell-rh/hypershell-stego/out/configuration"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
)

// Run supplies the domain controller to STEGO.
func Run(ctx context.Context, metrics *runtime.Metrics) error {
	api, err := settings.LoadControlAPI()
	if err != nil {
		return err
	}
	connection, err := rpc.New(rpc.Options{Address: api.Address, CAFile: api.CAFile, TokenFile: api.TokenFile})
	if err != nil {
		return err
	}
	defer connection.Close()
	controller, err := gatewaynetwork.New(pb.NewGatewayNetworkServiceClient(connection), pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection))
	if err != nil {
		return err
	}
	return controller.RunWithMetrics(ctx, metrics)
}
