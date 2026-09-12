// Package databaseapp connects domain providers to the generated worker.
package databaseapp

import (
	"context"
	"errors"
	"os"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
)

// Run supplies provider setup and the domain controller to STEGO.
func Run(ctx context.Context, metrics *runtime.Metrics) error {
	name := os.Getenv("DATABASE_PROVIDER")
	if name == "" {
		name = "deployment"
	}
	options := databasecontroller.KubernetesOptions{ControlNamespace: os.Getenv("HYPERSHELL_CONTROL_NAMESPACE"), ServerURL: os.Getenv("HYPERSHELL_KUBERNETES_URL"), CAFile: os.Getenv("HYPERSHELL_KUBERNETES_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_KUBERNETES_TOKEN_FILE"), ClusterIssuer: os.Getenv("HYPERSHELL_DATABASE_CLUSTER_ISSUER")}
	var provider interface {
		databasecontroller.Provider
		Close()
	}
	var err error
	switch name {
	case "deployment":
		provider, err = databasecontroller.NewKubernetes(options)
	case "cnpg":
		provider, err = databasecontroller.NewCNPG(options)
	default:
		return errors.New("database provider is not supported")
	}
	if err != nil {
		return err
	}
	defer provider.Close()
	connection, err := rpc.New(rpc.Options{Address: os.Getenv("HYPERSHELL_API_GRPC_ADDR"), CAFile: os.Getenv("HYPERSHELL_API_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_API_TOKEN_FILE")})
	if err != nil {
		return err
	}
	defer connection.Close()
	controller, err := databasecontroller.NewForProvider(pb.NewManagedDatabaseServiceClient(connection), control.NewDatabaseCleanupServiceClient(connection), name, os.Getenv("HYPERSHELL_MANAGED_CLUSTER_ID"), provider)
	if err != nil {
		return err
	}
	return controller.RunWithMetrics(ctx, metrics)
}
