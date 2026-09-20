// Package namespaceallocationapp connects Hypershell state to STEGO allocation.
package namespaceallocationapp

import (
	"context"

	"github.com/jsell-rh/hypershell-stego/internal/namespaceallocation"
	settings "github.com/jsell-rh/hypershell-stego/out/configuration"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// Run supplies the domain adapter. STEGO owns allocation and process behavior.
func Run(ctx context.Context, metrics *runtime.Metrics) error {
	api, err := settings.LoadControlAPI()
	if err != nil {
		return err
	}
	cluster, err := settings.LoadClusterWorker()
	if err != nil {
		return err
	}
	options, err := settings.LoadNamespaceWorker()
	if err != nil {
		return err
	}

	client, err := kube.New(kube.Options{ServerURL: cluster.ServerURL, CAFile: cluster.CAFile, TokenFile: cluster.TokenFile})
	if err != nil {
		return err
	}
	defer client.Close()
	allocator, err := allocation.New(client, cluster.ControlNamespace)
	if err != nil {
		return err
	}
	connection, err := rpc.New(rpc.Options{Address: api.Address, CAFile: api.CAFile, TokenFile: api.TokenFile})
	if err != nil {
		return err
	}
	defer connection.Close()
	controller, err := namespaceallocation.New(cluster.ClusterID, allocator, pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), namespaceallocation.Options{ConsoleDomain: options.ConsoleDomain, SandboxEnabled: cluster.SandboxRuntimeClass != ""})
	if err != nil {
		return err
	}
	return controller.Run(ctx, metrics)
}
