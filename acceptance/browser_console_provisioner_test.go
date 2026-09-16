package acceptance

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// The workload can read console credentials only for its assigned cluster.
// Its token is separate from the API's account provisioning token.
func (w *browserGatewayWorkload) consoleProvisionerPolicy(key *rsa.PrivateKey) []string {
	w.t.Helper()
	if w.public == nil {
		return nil
	}
	domains, err := json.Marshal(map[string]string{w.f.cluster: w.public.Domain})
	if err != nil {
		w.t.Fatal(err)
	}
	grants, err := json.Marshal([]auth.Grant{{Issuer: "https://issuer.example", Subject: "console-workload", Resource: "Gateway", Operation: "read.console-credential", Target: w.f.cluster}})
	if err != nil {
		w.t.Fatal(err)
	}
	w.consoleTokenFile = filepath.Join(w.t.TempDir(), "console-workload-token")
	if err := os.WriteFile(w.consoleTokenFile, []byte(token(w.t, key, "console-workload")), 0600); err != nil {
		w.t.Fatal(err)
	}
	return []string{"HYPERSHELL_GATEWAY_CONSOLE_DOMAINS=" + string(domains), "HYPERSHELL_CONSOLE_CREDENTIAL_GRANTS=" + string(grants)}
}

func (w *browserGatewayWorkload) checkConsoleProvisionerAccess(key *rsa.PrivateKey) {
	w.t.Helper()
	if w.public == nil {
		return
	}
	options := rpc.Options{TokenFile: filepath.Join(w.t.TempDir(), "console-probe-token")}
	for _, entry := range w.consoleProvisioner {
		name, value, _ := strings.Cut(entry, "=")
		switch name {
		case "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_ADDR":
			options.Address = value
		case "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_CA_FILE":
			options.CAFile = value
		}
	}
	if err := os.WriteFile(options.TokenFile, []byte(token(w.t, key, "console-workload")), 0600); err != nil {
		w.t.Fatal(err)
	}
	connection, err := rpc.New(options)
	if err != nil {
		w.t.Fatal("console probe client setup failed")
	}
	defer connection.Close()
	client := pb.NewGatewayConsoleCredentialServiceClient(connection)
	for _, probe := range []struct {
		subject, cluster string
		want             codes.Code
	}{
		{"console-workload", w.f.cluster, codes.InvalidArgument},
		{"console-workload", ksuid.New().String(), codes.PermissionDenied},
		{"api-provisioner", w.f.cluster, codes.PermissionDenied},
	} {
		if err := os.WriteFile(options.TokenFile, []byte(token(w.t, key, probe.subject)), 0600); err != nil {
			w.t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		// No Gateway ID means that an allowed probe stops before provider I/O.
		_, err := client.GetCredentials(ctx, &pb.GatewayConsoleCredentialRequest{ClusterId: probe.cluster})
		cancel()
		if status.Code(err) != probe.want {
			w.t.Fatal("console credential scope check failed", status.Code(err), probe.want)
		}
	}
}

func (w *browserGatewayWorkload) setConsoleProvisioner(settings []string) {
	w.t.Helper()
	if w.public == nil {
		return
	}
	if w.consoleTokenFile == "" {
		w.t.Fatal("console workload token is missing")
	}
	for _, entry := range settings {
		name, value, found := strings.Cut(entry, "=")
		if !found || value == "" {
			w.t.Fatal("provisioner connection setting is invalid")
		}
		switch name {
		case "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_ADDR", "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_CA_FILE":
			w.consoleProvisioner = append(w.consoleProvisioner, entry)
		case "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_TOKEN_FILE":
			w.consoleProvisioner = append(w.consoleProvisioner, name+"="+w.consoleTokenFile)
		default:
			w.t.Fatal("provisioner connection setting is unknown")
		}
	}
	if len(w.consoleProvisioner) != 3 {
		w.t.Fatal("provisioner connection settings are incomplete")
	}
}
