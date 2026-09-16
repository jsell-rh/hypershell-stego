package acceptance

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccountprovisioner"
	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func readConsoleCredentialThroughRuntime(t *testing.T, k *keycloakFixture, address, ca, readerToken, workerToken, workerSubject string, settings []string, id, cluster string) string {
	t.Helper()
	tokenFile := filepath.Join(t.TempDir(), "console-reader")
	if err := os.WriteFile(tokenFile, []byte(readerToken), 0600); err != nil {
		t.Fatal(err)
	}
	connection, err := rpc.New(rpc.Options{Address: address, CAFile: ca, TokenFile: tokenFile})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	state := control.NewGatewayIdentityServiceClient(connection)
	keys, err := os.ReadFile(k.stateKeysFile)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := runtime.NewStateProtectorFromJSON(keys)
	clear(keys)
	if err != nil {
		t.Fatal(err)
	}
	options := k.options
	options.ConsoleDomains = map[string]string{cluster: "console.example.com"}
	options.GatewayJournal = func(string, int64, bool) (*runtime.StateJournal, error) {
		return nil, errors.New("console reader cannot write native state")
	}
	options.ConsoleJournal = func(id string, revision int64, cleanup bool) (*runtime.StateJournal, error) {
		if cleanup {
			return nil, errors.New("console reader cannot close state")
		}
		return gatewayidentity.NewConsoleProviderStateJournal(state, protector, k.instanceID, id, revision, false)
	}
	client, err := keycloak.NewClient(options)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	policy, err := auth.NewGrantPolicy([]auth.Grant{{Issuer: k.options.ServerURL + "/realms/workflow", Subject: workerSubject, Resource: "Gateway", Operation: "read.console-credential", Target: cluster}})
	if err != nil {
		t.Fatal(err)
	}
	service := serviceaccountprovisioner.NewConsoleCredentialServer(client, state, policy)
	env, _, stop := startAuthenticatedProvisionerTransport(t, func(registrar grpc.ServiceRegistrar) error {
		pb.RegisterGatewayConsoleCredentialServiceServer(registrar, service)
		return nil
	}, settings, workerToken)
	defer stop()
	values := map[string]string{}
	for _, entry := range env {
		name, value, _ := strings.Cut(entry, "=")
		values[name] = value
	}
	delivery, err := rpc.New(rpc.Options{Address: values["HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_ADDR"], CAFile: values["HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_CA_FILE"], TokenFile: values["HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_TOKEN_FILE"]})
	if err != nil {
		t.Fatal(err)
	}
	defer delivery.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// This identity has no native state or write grant on the real API.
	if _, err := state.LoadGatewayProviderState(ctx, &control.LoadGatewayProviderStateRequest{GatewayId: id}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("console reader reached native state", err)
	}
	before, err := state.LoadGatewayProviderState(ctx, &control.LoadGatewayProviderStateRequest{GatewayId: id, ClientKind: control.GatewayIdentityClientKind_GATEWAY_IDENTITY_CLIENT_KIND_CONSOLE})
	if err != nil {
		t.Fatal("read console journal", err)
	}
	observation, err := state.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: id})
	if err != nil {
		t.Fatal(err)
	}
	saveCtx, err := rpc.WithResourceVersion(ctx, observation.ResourceVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := state.SaveGatewayProviderState(saveCtx, &control.SaveGatewayProviderStateRequest{GatewayId: id, ClientKind: control.GatewayIdentityClientKind_GATEWAY_IDENTITY_CLIENT_KIND_CONSOLE, ExpectedVersion: before.Version, SealedState: before.SealedState}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("console reader acquired write permission", err)
	}
	response, err := pb.NewGatewayConsoleCredentialServiceClient(delivery).GetCredentials(ctx, &pb.GatewayConsoleCredentialRequest{GatewayId: id, ClusterId: cluster, ResourceVersion: observation.ResourceVersion})
	if err != nil || response.GetClientSecret() == "" {
		t.Fatal("read real console credential through generated runtime", err)
	}
	after, err := state.LoadGatewayProviderState(ctx, &control.LoadGatewayProviderStateRequest{GatewayId: id, ClientKind: control.GatewayIdentityClientKind_GATEWAY_IDENTITY_CLIENT_KIND_CONSOLE})
	if err != nil {
		t.Fatal(err)
	}
	if before.Version != after.Version {
		t.Fatal("credential read changed the protected journal")
	}
	return response.ClientSecret
}
