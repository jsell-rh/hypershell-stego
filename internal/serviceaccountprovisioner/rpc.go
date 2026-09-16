package serviceaccountprovisioner

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	transport "github.com/jsell-rh/hypershell-stego/out/application/client"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
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
	connection, err := rpc.New(rpc.Options{Address: os.Getenv("HYPERSHELL_API_GRPC_ADDR"), CAFile: os.Getenv("HYPERSHELL_API_CA_FILE"), TokenFile: os.Getenv("HYPERSHELL_API_TOKEN_FILE")})
	if err != nil {
		return nil, err
	}
	ready := false
	defer func() {
		if !ready {
			connection.Close()
		}
	}()
	keys, err := transport.ReadPrivateFile(os.Getenv("HYPERSHELL_IDENTITY_STATE_KEYS_FILE"))
	if err != nil {
		return nil, errors.New("account provider state key file is unavailable")
	}
	protector, err := runtime.NewStateProtectorFromJSON(keys)
	clear(keys)
	if err != nil {
		return nil, err
	}
	instance := os.Getenv("HYPERSHELL_INSTANCE_ID")
	if len(instance) < 1 || len(instance) > 128 || strings.IndexFunc(instance, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.')
	}) >= 0 {
		return nil, errors.New("account provider requires a stable instance ID")
	}
	state := control.NewServiceAccountProviderStateServiceClient(connection)
	provider, err := keycloak.NewClient(keycloak.Options{
		ServerURL: os.Getenv("HYPERSHELL_KEYCLOAK_URL"), Realm: os.Getenv("HYPERSHELL_KEYCLOAK_REALM"), ClientID: os.Getenv("HYPERSHELL_KEYCLOAK_CLIENT_ID"), SecretFile: os.Getenv("HYPERSHELL_KEYCLOAK_SECRET_FILE"), CAFile: os.Getenv("HYPERSHELL_KEYCLOAK_CA_FILE"),
		AccountJournal: func(gatewayID, accountID string, cleanup bool) (*runtime.StateJournal, error) {
			return NewProviderStateJournal(state, protector, instance, gatewayID, accountID, cleanup)
		},
	})
	if err != nil {
		return nil, err
	}
	server, err := NewServer(provider, subjects)
	if err != nil {
		provider.Close()
		return nil, err
	}
	ready = true
	return &application{provider: provider, server: server, connection: connection}, nil
}

type application struct {
	connection *rpc.Client
	provider   *keycloak.Client
	server     *Server
}

func (a *application) Register(registrar grpc.ServiceRegistrar) error {
	pb.RegisterOpenShellGatewayServiceAccountProvisionerServiceServer(registrar, a.server)
	return nil
}
func (a *application) Close() error { a.provider.Close(); a.connection.Close(); return nil }
