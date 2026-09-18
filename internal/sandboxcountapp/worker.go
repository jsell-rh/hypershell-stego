// Package sandboxcountapp connects domain providers to the generated worker.
package sandboxcountapp

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/sandboxcount"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// Run supplies provider setup and the domain controller to STEGO.
func Run(ctx context.Context, metrics *runtime.Metrics) error {
	watchLimit := 0
	if value := os.Getenv("HYPERSHELL_SANDBOX_COUNT_WATCH_LIMIT"); value != "" {
		var err error
		watchLimit, err = strconv.Atoi(value)
		if err != nil {
			return err
		}
	}
	source, err := kube.New(kube.Options{ServerURL: os.Getenv("HYPERSHELL_KUBERNETES_URL"), CAFile: os.Getenv("HYPERSHELL_KUBERNETES_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_KUBERNETES_TOKEN_FILE"), WatchLimit: watchLimit})
	if err != nil {
		return err
	}
	defer source.Close()
	namespaces, err := allocation.New(source, os.Getenv("HYPERSHELL_CONTROL_NAMESPACE"))
	if err != nil {
		return err
	}
	connection, err := rpc.New(rpc.Options{Address: os.Getenv("HYPERSHELL_API_GRPC_ADDR"), CAFile: os.Getenv("HYPERSHELL_API_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_API_TOKEN_FILE")})
	if err != nil {
		return err
	}
	defer connection.Close()
	var resync time.Duration
	if value := os.Getenv("HYPERSHELL_SANDBOX_COUNT_RESYNC"); value != "" {
		resync, err = time.ParseDuration(value)
		if err != nil {
			return err
		}
	}
	controller, err := sandboxcount.New(source, namespaces, pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), os.Getenv("HYPERSHELL_MANAGED_CLUSTER_ID"), resync, sandboxcount.Options{SandboxEnabled: os.Getenv("HYPERSHELL_GATEWAY_SANDBOX_RUNTIME_CLASS") != ""})
	if err != nil {
		return err
	}
	return controller.RunWithMetrics(ctx, metrics)
}
