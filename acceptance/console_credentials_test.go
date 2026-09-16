package acceptance

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/serviceaccountprovisioner"
	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	model "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	provider "github.com/jsell-rh/hypershell-stego/out/keycloak"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type consoleCredentialState func(context.Context, *control.GetGatewayIdentityStateRequest, ...grpc.CallOption) (*control.GetGatewayIdentityStateResponse, error)

func (f consoleCredentialState) GetGatewayIdentityState(ctx context.Context, r *control.GetGatewayIdentityStateRequest, opts ...grpc.CallOption) (*control.GetGatewayIdentityStateResponse, error) {
	return f(ctx, r, opts...)
}

type consoleCredentialProvider func(context.Context, string, string, string, int64) (string, provider.Secret, error)

func (f consoleCredentialProvider) GatewayConsoleCredentials(ctx context.Context, id, name, cluster string, revision int64) (string, provider.Secret, error) {
	return f(ctx, id, name, cluster, revision)
}

func consoleTestSecret(t *testing.T) provider.Secret {
	t.Helper()
	binding := provider.ClientBinding{ID: "fixture-client", ClientID: "console-fixture", Attributes: map[string]string{"stego.owner.fixture": "true"}}
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/realms/test/protocol/openid-connect/token":
			_, _ = w.Write([]byte(`{"access_token":"test-admin-token","expires_in":300,"token_type":"Bearer"}`))
		case "/admin/realms/test/clients/fixture-client":
			_ = json.NewEncoder(w).Encode(map[string]any{"id": binding.ID, "clientId": binding.ClientID, "attributes": binding.Attributes, "protocol": "openid-connect", "publicClient": false})
		case "/admin/realms/test/clients/fixture-client/client-secret":
			_, _ = w.Write([]byte(`{"value":"test-console-credential"}`))
		default:
			t.Error("unexpected credential fixture request")
			w.WriteHeader(500)
		}
	}))
	defer server.Close()
	dir := t.TempDir()
	ca, secret := filepath.Join(dir, "ca"), filepath.Join(dir, "secret")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(secret, []byte("test-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := provider.New(provider.Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	value, err := client.GetClientSecret(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestConsoleCredentialDeliveryChecksCurrentAssignment(t *testing.T) {
	secret := consoleTestSecret(t)
	key, settings := issuer(t)
	id, cluster := ksuid.New().String(), ksuid.New().String()
	base := &control.GetGatewayIdentityStateResponse{Gateway: &model.Gateway{Metadata: &model.ObjectReference{Id: id}, Name: "gateway", ClusterId: cluster}, ResourceVersion: 7}
	policy, err := auth.NewGrantPolicy([]auth.Grant{{Issuer: "https://issuer.example", Subject: "api-provisioner", Resource: "Gateway", Operation: "read.console-credential", Target: cluster}})
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"valid", "ungranted-cluster", "ungranted-subject", "invalid-revision", "stale", "deleted", "wrong-cluster", "wrong-id", "read-failure", "provider-failure", "changed-after-read", "deleted-after-read", "invalid-binding"} {
		t.Run(mode, func(t *testing.T) {
			var reads, calls atomic.Int32
			state := consoleCredentialState(func(_ context.Context, r *control.GetGatewayIdentityStateRequest, _ ...grpc.CallOption) (*control.GetGatewayIdentityStateResponse, error) {
				n := reads.Add(1)
				if r.Id != id {
					t.Error("wrong resource lookup")
				}
				value := proto.Clone(base).(*control.GetGatewayIdentityStateResponse)
				switch mode {
				case "stale":
					value.ResourceVersion++
				case "deleted":
					value.Deleted = true
				case "wrong-cluster":
					value.Gateway.ClusterId = ksuid.New().String()
				case "wrong-id":
					value.Gateway.Metadata.Id = ksuid.New().String()
				case "read-failure":
					return nil, errors.New("private state failure")
				case "changed-after-read":
					if n == 2 {
						value.ResourceVersion++
					}
				case "deleted-after-read":
					if n == 2 {
						value.Deleted = true
					}
				}
				return value, nil
			})
			client := consoleCredentialProvider(func(_ context.Context, gotID, name, gotCluster string, revision int64) (string, provider.Secret, error) {
				calls.Add(1)
				if gotID != id || name != "gateway" || gotCluster != cluster || revision != 7 {
					t.Error("provider received a wrong observation")
				}
				if mode == "provider-failure" {
					return "", provider.Secret{}, errors.New("private provider failure")
				}
				if mode == "invalid-binding" {
					return "wrong-client", secret, nil
				}
				return "hs-console-" + id, secret, nil
			})
			service := serviceaccountprovisioner.NewConsoleCredentialServer(client, state, policy)
			options, bearerFile, stop := startProvisionerTransport(t, func(r grpc.ServiceRegistrar) error {
				pb.RegisterGatewayConsoleCredentialServiceServer(r, service)
				return nil
			}, key, settings)
			defer stop()
			if mode == "ungranted-subject" {
				if err := os.WriteFile(bearerFile, []byte(token(t, key, "other-worker")), 0600); err != nil {
					t.Fatal(err)
				}
			}
			env := map[string]string{}
			for _, entry := range options {
				for i, ch := range entry {
					if ch == '=' {
						env[entry[:i]] = entry[i+1:]
						break
					}
				}
			}
			connection, err := rpc.New(rpc.Options{Address: env["HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_ADDR"], CAFile: env["HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_CA_FILE"], TokenFile: env["HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_TOKEN_FILE"]})
			if err != nil {
				t.Fatal(err)
			}
			defer connection.Close()
			request := &pb.GatewayConsoleCredentialRequest{GatewayId: id, ClusterId: cluster, ResourceVersion: 7}
			if mode == "invalid-revision" {
				request.ResourceVersion = 0
			}
			if mode == "ungranted-cluster" {
				request.ClusterId = ksuid.New().String()
			}
			result, err := pb.NewGatewayConsoleCredentialServiceClient(connection).GetCredentials(context.Background(), request)
			if mode == "valid" {
				if err != nil || result.GetClientId() != "hs-console-"+id || result.GetClientSecret() != secret.Reveal() {
					t.Fatal("valid credential was not delivered")
				}
				return
			}
			if err == nil || result != nil {
				t.Fatal("invalid request received a credential")
			}
			if (mode == "ungranted-cluster" || mode == "ungranted-subject") && (status.Code(err) != codes.PermissionDenied || reads.Load() != 0 || calls.Load() != 0) {
				t.Fatal("denied worker reached provider state")
			}
			if strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), secret.Reveal()) {
				t.Fatal("credential failure exposed provider data")
			}
			if mode == "invalid-revision" || mode == "stale" || mode == "deleted" || mode == "wrong-cluster" || mode == "wrong-id" || mode == "read-failure" {
				if calls.Load() != 0 {
					t.Fatal("invalid observation reached provider")
				}
			}
			if mode == "changed-after-read" || mode == "deleted-after-read" {
				if reads.Load() != 2 || calls.Load() != 1 {
					t.Fatal("credential did not require a second observation")
				}
			}
		})
	}
}
