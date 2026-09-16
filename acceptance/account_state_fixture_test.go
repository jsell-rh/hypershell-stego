package acceptance

import (
	"context"
	"crypto/rsa"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	api "github.com/jsell-rh/hypershell-stego/internal/grpcapi"
	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	transport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
	"google.golang.org/grpc"
)

// This authenticated API replica shares the real database. It permits the
// provisioner to start before the public API receives its test listener address.
// It uses the production journal handlers and does not replace persistence.
func startAccountStateAPI(t *testing.T, f *fixture, k *keycloakFixture, key *rsa.PrivateKey, settings []string, host, listen string) []string {
	t.Helper()
	for _, entry := range settings {
		name, value, _ := strings.Cut(entry, "=")
		old, present := os.LookupEnv(name)
		t.Setenv(name, value)
		defer func() {
			if present {
				os.Setenv(name, old)
			} else {
				os.Unsetenv(name)
			}
		}()
	}
	verifier, err := auth.NewVerifierFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	policy, err := auth.NewGrantPolicy([]auth.Grant{{Issuer: "https://issuer.example", Subject: "account-worker", Resource: "ServiceAccount", Operation: "provider-state"}})
	if err != nil {
		t.Fatal(err)
	}
	service, err := gateways.New(f.storage, gateways.Options{ControlPlaneSubjects: []string{"account-worker"}, ProviderStatePolicy: policy})
	if err != nil {
		t.Fatal(err)
	}
	id := identity(t, host)
	directory := filepath.Dir(id.config.CAFile)
	for name, value := range map[string]string{"STEGO_GRPC_ADDR": listen, "STEGO_GRPC_TLS_CERT": filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY": filepath.Join(directory, "server-key.pem")} {
		old, present := os.LookupEnv(name)
		t.Setenv(name, value)
		defer func() {
			if present {
				os.Setenv(name, old)
			} else {
				os.Unsetenv(name)
			}
		}()
	}
	server, err := transport.New(verifier.Authenticate, func(registrar grpc.ServiceRegistrar) error {
		api.RegisterAccountProviderState(registrar, service)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		server.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("account state API did not stop")
		}
	})
	_, port, err := net.SplitHostPort(server.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(t.TempDir(), "account-state-token")
	if err := os.WriteFile(tokenFile, []byte(token(t, key, "account-worker")), 0600); err != nil {
		t.Fatal(err)
	}
	return []string{"HYPERSHELL_API_GRPC_ADDR=" + net.JoinHostPort(host, port), "HYPERSHELL_API_CA_FILE=" + id.config.CAFile, "HYPERSHELL_API_TOKEN_FILE=" + tokenFile, "HYPERSHELL_IDENTITY_STATE_KEYS_FILE=" + k.stateKeysFile, "HYPERSHELL_INSTANCE_ID=" + k.instanceID}
}
