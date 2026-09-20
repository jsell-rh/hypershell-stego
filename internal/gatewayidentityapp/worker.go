// Package gatewayidentityapp connects domain providers to the generated worker.
package gatewayidentityapp

import (
	"context"
	"errors"
	transport "github.com/jsell-rh/hypershell-stego/out/application/client"
	"os"
	"strings"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	settings "github.com/jsell-rh/hypershell-stego/out/configuration"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
)

// Run supplies provider setup and the domain controller to STEGO.
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
	keys, err := transport.ReadPrivateFile(os.Getenv("HYPERSHELL_IDENTITY_STATE_KEYS_FILE"))
	if err != nil {
		return errors.New("Gateway identity state key file is unavailable")
	}
	protector, err := runtime.NewStateProtectorFromJSON(keys)
	clear(keys)
	if err != nil {
		return err
	}
	state := control.NewGatewayIdentityServiceClient(connection)
	instance := os.Getenv("HYPERSHELL_INSTANCE_ID")
	if len(instance) < 1 || len(instance) > 128 || strings.IndexFunc(instance, func(r rune) bool {
		return !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.')
	}) >= 0 {
		return errors.New("Gateway identity requires a stable instance ID")
	}
	domains, err := keycloak.ParseConsoleDomains(os.Getenv("HYPERSHELL_GATEWAY_CONSOLE_DOMAINS"))
	if err != nil {
		return err
	}
	provider, err := keycloak.NewClient(keycloak.Options{ConsoleDomains: domains,
		ConsoleJournal: func(id string, revision int64, cleanup bool) (*runtime.StateJournal, error) {
			return gatewayidentity.NewConsoleProviderStateJournal(state, protector, instance, id, revision, cleanup)
		}, ServerURL: os.Getenv("HYPERSHELL_KEYCLOAK_URL"), Realm: os.Getenv("HYPERSHELL_KEYCLOAK_REALM"), ClientID: os.Getenv("HYPERSHELL_KEYCLOAK_CLIENT_ID"), SecretFile: os.Getenv("HYPERSHELL_KEYCLOAK_SECRET_FILE"), CAFile: os.Getenv("HYPERSHELL_KEYCLOAK_CA_FILE"),
		GatewayJournal: func(id string, revision int64, cleanup bool) (*runtime.StateJournal, error) {
			return gatewayidentity.NewProviderStateJournal(state, protector, instance, id, revision, cleanup)
		},
	})
	if err != nil {
		return err
	}
	defer provider.Close()
	controller, err := gatewayidentity.New(pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), provider)
	if err != nil {
		return err
	}
	return controller.RunWithMetrics(ctx, metrics)
}
