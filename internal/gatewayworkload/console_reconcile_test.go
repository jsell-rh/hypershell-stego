package gatewayworkload

import (
	"context"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	provisioner "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func TestConsoleReconciliationCreatesVerifiedDependencies(t *testing.T) {
	for _, mode := range []string{"valid", "server pending", "client pending", "foreign origin", "wrong version", "extra data", "credential denied", "untrusted server"} {
		t.Run(mode, func(t *testing.T) {
			gw, release := records(t)
			origin, err := keycloak.GatewayConsoleOrigin(gw.Metadata.Id, "example.test")
			if err != nil {
				t.Fatal(err)
			}
			gw.ConsoleAddress = &origin
			public, serverCert, serverKey := publicTestCertificate(t, strings.TrimPrefix(origin, "https://"), time.Now().Add(time.Hour), x509.ExtKeyUsageServerAuth)
			internal, clientCert, clientKey := publicTestCertificate(t, Name+"."+gw.Namespace+".svc.cluster.local", time.Now().Add(time.Hour), x509.ExtKeyUsageClientAuth)
			encode := func(value []byte) string { return base64.StdEncoding.EncodeToString(value) }
			tlsSecret := func(name string, cert, key []byte, owner kube.Owner) object {
				labels := object{}
				for key, value := range owner {
					labels[key] = value
				}
				return object{"apiVersion": "v1", "kind": "Secret", "type": "kubernetes.io/tls", "metadata": object{"namespace": gw.Namespace, "name": name, "uid": "uid-" + name, "resourceVersion": "1", "labels": labels}, "data": object{"tls.crt": encode(cert), "tls.key": encode(key)}}
			}
			secretsPath := "/api/v1/namespaces/" + gw.Namespace + "/secrets"
			certificatesPath := "/apis/cert-manager.io/v1/namespaces/" + gw.Namespace + "/certificates"
			stored := map[string]object{secretsPath + "/" + consoleName + "-tls": tlsSecret(consoleName+"-tls", serverCert, serverKey, consoleOwner(gw.Metadata.Id)), secretsPath + "/openshell-client-tls": tlsSecret("openshell-client-tls", clientCert, clientKey, owner(gw.Metadata.Id))}
			if mode == "server pending" {
				delete(stored, secretsPath+"/"+consoleName+"-tls")
			}
			if mode == "client pending" {
				delete(stored, secretsPath+"/openshell-client-tls")
			}
			var mu sync.Mutex
			calls, writes, dependencyWrites := 0, 0, 0
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls++
				if r.Method == http.MethodGet {
					value := stored[r.URL.Path]
					if value == nil {
						w.WriteHeader(404)
						return
					}
					_ = json.NewEncoder(w).Encode(value)
					return
				}
				if r.Method != http.MethodPost || (r.URL.Path != secretsPath && r.URL.Path != certificatesPath) {
					t.Error("unexpected console write")
					w.WriteHeader(500)
					return
				}
				writes++
				var value object
				if json.NewDecoder(r.Body).Decode(&value) != nil {
					t.Error("invalid object")
					w.WriteHeader(500)
					return
				}
				name := kube.String(value, "metadata", "name")
				meta := value["metadata"].(map[string]any)
				meta["uid"] = "uid-" + name
				meta["resourceVersion"] = "1"
				if r.URL.Path == secretsPath {
					dependencyWrites++
					if mode == "extra data" {
						value["data"].(map[string]any)["DATABASE_URL"] = "aW5qZWN0ZWQ="
					}
				} else {
					if kube.String(value, "spec", "issuerRef", "name") != "public-issuer" || kube.String(value, "spec", "secretName") != consoleName+"-tls" || !consoleOwner(gw.Metadata.Id).Matches(value) {
						t.Error("console certificate lost its configured identity")
					}
					names, _ := kube.Nested(value, "spec", "dnsNames").([]any)
					if len(names) != 1 || names[0] != strings.TrimPrefix(origin, "https://") {
						t.Error("certificate has an unexpected DNS name")
					}
				}
				stored[r.URL.Path+"/"+name] = value
				_ = json.NewEncoder(w).Encode(value)
			})
			k.publicRoots = x509.NewCertPool()
			k.publicRoots.AppendCertsFromPEM(public)
			k.internalRoots = x509.NewCertPool()
			k.internalRoots.AppendCertsFromPEM(internal)
			k.internalTrust = internal
			k.options.PublicDomain = "example.test"
			k.options.PublicIssuer = "public-issuer"
			k.options.PublicRouter = "default"
			credentialsRead := 0
			k.options.Console = &ConsoleOptions{Domain: "example.test", Image: release.Image, Credentials: consoleCredentialFixture{read: func(_ context.Context, r *provisioner.GatewayConsoleCredentialRequest) (*provisioner.GatewayConsoleCredentialResponse, error) {
				credentialsRead++
				if r.GatewayId != gw.Metadata.Id || r.ClusterId != gw.ClusterId || r.ResourceVersion != 42 {
					t.Error("credential request lost the current observation")
				}
				if mode == "credential denied" {
					return nil, errors.New("private-denial")
				}
				return &provisioner.GatewayConsoleCredentialResponse{ClientId: "hs-console-" + gw.Metadata.Id, ClientSecret: "valid-fixture-client-secret"}, nil
			}}}
			version := int64(42)
			if mode == "wrong version" {
				version = 0
			}
			if mode == "foreign origin" {
				foreign := "https://foreign.example.test"
				gw.ConsoleAddress = &foreign
			}
			if mode == "untrusted server" {
				k.publicRoots = x509.NewCertPool()
			}
			store := object{"database-url": encode([]byte("private-database-url")), "database-ca.pem": encode(public), "session-key": encode([]byte("retained-key"))}
			digest, err := k.ensureConsoleDependencies(context.Background(), gw, version, store)
			if mode != "valid" {
				if err == nil || digest != "" {
					t.Fatal("unverified dependencies accepted")
				}
				if strings.Contains(err.Error(), "private-") {
					t.Fatal("private value entered an error")
				}
				if strings.HasSuffix(mode, "pending") && !errors.Is(err, ErrPending) {
					t.Fatal("missing certificate did not wait", err)
				}
				mu.Lock()
				defer mu.Unlock()
				if (mode == "foreign origin" || mode == "wrong version") && calls != 0 {
					t.Fatal("invalid origin or observation reached Kubernetes")
				}
				if mode != "extra data" && dependencyWrites != 0 {
					t.Fatal("invalid dependency reached the runtime Secrets")
				}
				if (strings.HasSuffix(mode, "pending") || mode == "untrusted server") && credentialsRead != 0 {
					t.Fatal("unverified TLS reached credentials")
				}
				return
			}
			if err != nil || len(digest) != 64 || credentialsRead != 1 {
				t.Fatal("valid dependencies failed", err)
			}
			mu.Lock()
			initialWrites := writes
			mu.Unlock()
			repeated, err := k.ensureConsoleDependencies(context.Background(), gw, version, store)
			if err != nil || repeated != digest {
				t.Fatal("repeat reconciliation changed retained configuration", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if writes != initialWrites || dependencyWrites != 4 {
				t.Fatal("retained dependencies were written again")
			}
			dependencies := make([]object, 4)
			for i, name := range consoleSecretNames {
				dependencies[i] = stored[secretsPath+"/"+name]
			}
			if err := consoleStoreDependencies(dependencies, store); err != nil {
				t.Fatal(err)
			}
			if kube.String(dependencies[3], "data", "client.key") != encode(clientKey) || kube.String(dependencies[1], "data", "tls.key") != encode(serverKey) {
				t.Fatal("verified keys did not reach their separate mounts")
			}
		})
	}
}
