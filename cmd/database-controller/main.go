package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
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
	provider, err := databasecontroller.NewKubernetes(databasecontroller.KubernetesOptions{ServerURL: os.Getenv("HYPERSHELL_KUBERNETES_URL"), CAFile: os.Getenv("HYPERSHELL_KUBERNETES_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_KUBERNETES_TOKEN_FILE"), ClusterIssuer: os.Getenv("HYPERSHELL_DATABASE_CLUSTER_ISSUER")})
	if err != nil {
		return err
	}
	defer provider.Close()
	connection, err := rpc.New(rpc.Options{Address: os.Getenv("HYPERSHELL_API_GRPC_ADDR"), CAFile: os.Getenv("HYPERSHELL_API_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_API_TOKEN_FILE")})
	if err != nil {
		return err
	}
	defer connection.Close()
	controller, err := databasecontroller.New(pb.NewManagedDatabaseServiceClient(connection), control.NewDatabaseCleanupServiceClient(connection), provider)
	if err != nil {
		return err
	}
	return controller.Run(ctx)
}
