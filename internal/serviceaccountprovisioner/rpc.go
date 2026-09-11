package serviceaccountprovisioner

import (
	"context"
	"encoding/json"
	"errors"
	"os"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	process "github.com/jsell-rh/hypershell-stego/out/grpcapi/process"
	"google.golang.org/grpc"
)

// Open supplies the account provider and caller policy. STEGO owns the process.
func Open(ctx context.Context) (process.Application, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var subjects []string
	raw := os.Getenv("HYPERSHELL_PROVISIONER_SUBJECTS")
	if len(raw) > 16384 || json.Unmarshal([]byte(raw), &subjects) != nil {
		return nil, errors.New("provisioner subjects must be a JSON array")
	}
	provider, err := keycloak.NewClient(keycloak.Options{
		ServerURL: os.Getenv("HYPERSHELL_KEYCLOAK_URL"), Realm: os.Getenv("HYPERSHELL_KEYCLOAK_REALM"), ClientID: os.Getenv("HYPERSHELL_KEYCLOAK_CLIENT_ID"), SecretFile: os.Getenv("HYPERSHELL_KEYCLOAK_SECRET_FILE"), CAFile: os.Getenv("HYPERSHELL_KEYCLOAK_CA_FILE"),
	})
	if err != nil {
		return nil, err
	}
	server, err := NewServer(provider, subjects)
	if err != nil {
		provider.Close()
		return nil, err
	}
	return &application{provider: provider, server: server}, nil
}

type application struct {
	provider *keycloak.Client
	server   *Server
}

func (a *application) Register(registrar grpc.ServiceRegistrar) error {
	pb.RegisterOpenShellGatewayServiceAccountProvisionerServiceServer(registrar, a.server)
	return nil
}
func (a *application) Close() error { a.provider.Close(); return nil }
