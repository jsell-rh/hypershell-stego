package gatewayworkload

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	provisioner "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	"google.golang.org/grpc"
)

type consoleCredentialFixture struct {
	provisioner.GatewayConsoleCredentialServiceClient
	read func(context.Context, *provisioner.GatewayConsoleCredentialRequest) (*provisioner.GatewayConsoleCredentialResponse, error)
}

func (f consoleCredentialFixture) GetCredentials(ctx context.Context, r *provisioner.GatewayConsoleCredentialRequest, _ ...grpc.CallOption) (*provisioner.GatewayConsoleCredentialResponse, error) {
	return f.read(ctx, r)
}

func TestConsoleDependenciesUseCurrentCredentialsAndSeparateMounts(t *testing.T) {
	for _, scenario := range []string{"valid", "wrong version", "foreign origin", "foreign client", "short secret", "unknown response", "missing TLS identity", "denied", "canceled"} {
		t.Run(scenario, func(t *testing.T) {
			gw, _ := records(t)
			origin, err := keycloak.GatewayConsoleOrigin(gw.Metadata.Id, "example.test")
			if err != nil {
				t.Fatal(err)
			}
			gw.ConsoleAddress = &origin
			internal, clientCert, clientKey := publicTestCertificate(t, Name+"."+gw.Namespace+".svc.cluster.local", time.Now().Add(time.Hour), x509.ExtKeyUsageClientAuth)
			public, serverCert, serverKey := publicTestCertificate(t, strings.TrimPrefix(origin, "https://"), time.Now().Add(time.Hour), x509.ExtKeyUsageServerAuth)
			encode := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
			secret := func(name string, cert, key []byte, labels map[string]string) object {
				checkedLabels := object{}
				for name, value := range labels {
					checkedLabels[name] = value
				}
				return object{"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/tls", "metadata": object{"name": name, "namespace": gw.Namespace, "uid": "uid-" + name, "resourceVersion": "1", "labels": checkedLabels}, "data": object{"tls.crt": base64.StdEncoding.EncodeToString(cert), "tls.key": base64.StdEncoding.EncodeToString(key)}}
			}
			server := secret(consoleName+"-tls", serverCert, serverKey, consoleOwner(gw.Metadata.Id))
			client := secret("openshell-client-tls", clientCert, clientKey, owner(gw.Metadata.Id))
			reads := 0
			k := &Kubernetes{options: Options{ClusterID: gw.ClusterId, Issuer: "https://issuer.example.test/realm", Console: &ConsoleOptions{Domain: "example.test"}}, trust: string(public), internalTrust: internal, internalRoots: x509.NewCertPool(), publicRoots: x509.NewCertPool()}
			k.internalRoots.AppendCertsFromPEM(internal)
			k.publicRoots.AppendCertsFromPEM(public)
			k.options.Console.Credentials = consoleCredentialFixture{read: func(ctx context.Context, r *provisioner.GatewayConsoleCredentialRequest) (*provisioner.GatewayConsoleCredentialResponse, error) {
				reads++
				if r.GatewayId != gw.Metadata.Id || r.ClusterId != gw.ClusterId || r.ResourceVersion != 42 {
					t.Fatal("credential request lost its observation")
				}
				if scenario == "denied" {
					return nil, errors.New("private-provider-details")
				}
				response := &provisioner.GatewayConsoleCredentialResponse{ClientId: "hs-console-" + gw.Metadata.Id, ClientSecret: "console-test-client-secret"}
				switch scenario {
				case "foreign client":
					response.ClientId = "another-client"
				case "short secret":
					response.ClientSecret = "short"
				case "unknown response":
					response.ProtoReflect().SetUnknown([]byte{0x78, 1})
				}
				return response, nil
			}}
			version := int64(42)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch scenario {
			case "wrong version":
				version = 0
			case "foreign origin":
				bad := "https://other.example.test"
				gw.ConsoleAddress = &bad
			case "missing TLS identity":
				delete(server["metadata"].(object), "uid")
			case "canceled":
				cancel()
			}
			store := object{"database-url": encode("private-database-url"), "database-ca.pem": encode(string(public)), "session-key": encode("private-session-key")}
			got, err := k.consoleDependencyObjects(ctx, gw, version, store, server, client)
			if scenario != "valid" {
				if err == nil || got != nil {
					t.Fatal("unsafe console dependency accepted")
				}
				if strings.Contains(err.Error(), "private-provider-details") {
					t.Fatal("provider detail escaped")
				}
				if (scenario == "wrong version" || scenario == "foreign origin" || scenario == "missing TLS identity") && reads != 0 {
					t.Fatal("invalid observation reached credential service")
				}
				return
			}
			if err != nil || len(got) != 4 || reads != 1 {
				t.Fatal("valid dependencies failed", err)
			}
			if err := consoleStoreDependencies(got, store); err != nil {
				t.Fatal(err)
			}
			for _, s := range got[2:] {
				for _, value := range s["data"].(object) {
					if value == store["database-url"] || value == store["session-key"] || value == encode("console-test-client-secret") {
						t.Fatal("browser credential entered dashboard mount")
					}
				}
			}
			for _, s := range got[:2] {
				for _, value := range s["data"].(object) {
					if value == base64.StdEncoding.EncodeToString(clientKey) {
						t.Fatal("Gateway client key entered browser mount")
					}
				}
			}
			if len(got[3]["data"].(object)) != 3 {
				t.Fatal("unexpected dashboard file")
			}
		})
	}
}
