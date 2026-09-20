// Package sandboxcountapp connects domain providers to the generated worker.
package sandboxcountapp

import (
	"context"

	"github.com/jsell-rh/hypershell-stego/internal/sandboxcount"
	settings "github.com/jsell-rh/hypershell-stego/out/configuration"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
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
	options, err := settings.LoadSandboxCounter()
	if err != nil {
		return err
	}

	source, err := kube.New(kube.Options{ServerURL: cluster.ServerURL, CAFile: cluster.CAFile, TokenFile: cluster.TokenFile, WatchLimit: int(options.WatchLimit)})
	if err != nil {
		return err
	}
	defer source.Close()
	namespaces, err := allocation.New(source, cluster.ControlNamespace)
	if err != nil {
		return err
	}
	connection, err := rpc.New(rpc.Options{Address: api.Address, CAFile: api.CAFile, TokenFile: api.TokenFile})
	if err != nil {
		return err
	}
	defer connection.Close()
	controller, err := sandboxcount.New(source, namespaces, pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), cluster.ClusterID, options.Resync, sandboxcount.Options{SandboxEnabled: cluster.SandboxRuntimeClass != ""})
	if err != nil {
		return err
	}
	return controller.RunWithMetrics(ctx, metrics)
}
