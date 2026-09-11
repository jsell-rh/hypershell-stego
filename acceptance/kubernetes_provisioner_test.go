package acceptance

import (
	"context"
	"crypto/rsa"
	"os"
	"path/filepath"
	"testing"

	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func (p *kubernetesBrowser) startProvisioner(k *keycloakFixture, key *rsa.PrivateKey, settings []string) ([]string, func() string, func()) {
	p.t.Helper()
	name := "hypershell-provisioner"
	id := identity(p.t, p.host(name))
	settings = append(append([]string{}, settings...), "HYPERSHELL_KEYCLOAK_URL="+k.options.ServerURL, "HYPERSHELL_KEYCLOAK_REALM="+k.options.Realm, "HYPERSHELL_KEYCLOAK_CLIENT_ID="+k.options.ClientID, "HYPERSHELL_KEYCLOAK_SECRET_FILE="+k.options.SecretFile, "HYPERSHELL_KEYCLOAK_CA_FILE="+k.options.CAFile, `HYPERSHELL_PROVISIONER_SUBJECTS=["api-provisioner"]`)
	env, files := map[string]string{}, map[string][]byte{}
	p.settings(settings, env, files)
	tokenFile := filepath.Join(p.t.TempDir(), "service-token")
	if err := os.WriteFile(tokenFile, []byte(token(p.t, key, "api-provisioner")), 0600); err != nil {
		p.t.Fatal(err)
	}
	options := rpc.Options{Address: p.host(name) + ":9090", CAFile: id.config.CAFile, TokenFile: tokenFile}
	start := func() (func(), func() string) {
		stop, logs := p.start(name, "..", os.Getenv("STEGO_TEST_PROVISIONER_IMAGE"), id, env, files, "--rpc-process", "provisioner")
		checkProvisionerIdentity(p.t, key, options)
		return stop, logs
	}
	stop, logs := start()
	previous := ""
	restart := func() {
		stop()
		previous += logs()
		stop, logs = start()
	}
	p.t.Cleanup(func() { stop() })
	return []string{"HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_ADDR=" + options.Address, "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_CA_FILE=" + options.CAFile, "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_TOKEN_FILE=" + tokenFile}, func() string { return previous + logs() }, restart
}

// Use a request with no specification. An allowed caller gets InvalidArgument
// before any provider write. The same endpoint must reject other identities.
func checkProvisionerIdentity(t *testing.T, key *rsa.PrivateKey, options rpc.Options) {
	t.Helper()
	// These probes have their own token file. They cannot change the API token.
	options.TokenFile = filepath.Join(t.TempDir(), "probe-token")
	if err := os.WriteFile(options.TokenFile, []byte(token(t, key, "api-provisioner")), 0600); err != nil {
		t.Fatal(err)
	}
	connection, err := rpc.New(options)
	if err != nil {
		t.Fatal("RPC client setup failed")
	}
	defer connection.Close()
	client := pb.NewOpenShellGatewayServiceAccountProvisionerServiceClient(connection)
	for _, probe := range []struct {
		value string
		want  codes.Code
	}{{token(t, key, "api-provisioner"), codes.InvalidArgument}, {token(t, key, "another-service"), codes.PermissionDenied}, {"invalid", codes.Unauthenticated}} {
		if err := os.WriteFile(options.TokenFile, []byte(probe.value), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := client.Provision(context.Background(), &pb.ProvisionRequest{})
		if status.Code(err) != probe.want {
			t.Fatal("deployed RPC identity rule failed", status.Code(err), probe.want)
		}
	}
}
