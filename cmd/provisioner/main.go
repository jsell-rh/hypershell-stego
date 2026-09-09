// The provisioner composes Hypershell rules with the generated secure runtime.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	provisioner "github.com/jsell-rh/hypershell-stego/internal/serviceaccountprovisioner"
	"github.com/jsell-rh/hypershell-stego/out/auth"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	"github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
	"google.golang.org/grpc"
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
	provider, err := keycloak.NewClient(keycloak.Options{
		ServerURL: os.Getenv("HYPERSHELL_KEYCLOAK_URL"), Realm: os.Getenv("HYPERSHELL_KEYCLOAK_REALM"), ClientID: os.Getenv("HYPERSHELL_KEYCLOAK_CLIENT_ID"), SecretFile: os.Getenv("HYPERSHELL_KEYCLOAK_SECRET_FILE"), CAFile: os.Getenv("HYPERSHELL_KEYCLOAK_CA_FILE"),
	})
	if err != nil {
		return err
	}
	defer provider.Close()
	var subjects []string
	raw := os.Getenv("HYPERSHELL_PROVISIONER_SUBJECTS")
	if len(raw) > 16384 || json.Unmarshal([]byte(raw), &subjects) != nil {
		return errors.New("provisioner subjects must be a JSON array")
	}
	server, err := provisioner.NewServer(provider, subjects)
	if err != nil {
		return err
	}
	verifier, err := auth.NewVerifierFromEnvironment()
	if err != nil {
		return err
	}
	runtime, err := transport.New(verifier.Authenticate, func(registrar grpc.ServiceRegistrar) error {
		pb.RegisterOpenShellGatewayServiceAccountProvisionerServiceServer(registrar, server)
		return nil
	})
	if err != nil {
		return err
	}
	defer runtime.Close()
	return runtime.Run(ctx)
}
