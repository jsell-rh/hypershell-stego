package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/sandboxcount"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
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
	source, err := kube.New(kube.Options{ServerURL: os.Getenv("HYPERSHELL_KUBERNETES_URL"), CAFile: os.Getenv("HYPERSHELL_KUBERNETES_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_KUBERNETES_TOKEN_FILE")})
	if err != nil {
		return err
	}
	defer source.Close()
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
	controller, err := sandboxcount.New(source, pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), os.Getenv("HYPERSHELL_MANAGED_CLUSTER_ID"), resync)
	if err != nil {
		return err
	}
	return controller.Run(ctx)
}
