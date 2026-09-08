package acceptance

import (
	"context"
	"crypto/rsa"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/contracts"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	"github.com/jsell-rh/hypershell-stego/out/auth"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/provisioner/v1"
	transport "github.com/jsell-rh/hypershell-stego/out/grpcapi/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
)

type accountRPC struct {
	pb.UnimplementedOpenShellGatewayServiceAccountProvisionerServiceServer
	provider *accountProvider
}

func provisionerCaller(ctx context.Context) error {
	if auth.IdentityFromContext(ctx).UserID != "api-provisioner" {
		return status.Error(codes.PermissionDenied, "caller is not permitted")
	}
	return nil
}
func (s *accountRPC) Provision(ctx context.Context, r *pb.ProvisionRequest) (*pb.ProvisionResponse, error) {
	if err := provisionerCaller(ctx); err != nil {
		return nil, err
	}
	spec := r.GetSpec()
	if spec == nil {
		return nil, status.Error(codes.InvalidArgument, "specification is required")
	}
	result, err := s.provider.Provision(ctx, serviceaccounts.Spec{ClientID: spec.ClientId, DisplayName: spec.DisplayName, GatewayClientID: spec.GatewayClientId, GatewayID: spec.GatewayId, ServiceAccountID: spec.ServiceAccountId, CreatorUserID: spec.CreatorUserId, Role: spec.Role, ExpectedIssuer: spec.ExpectedIssuer, AccessTokenLifetimeSeconds: spec.AccessTokenLifetimeSeconds})
	if err != nil {
		return nil, status.Error(codes.Unavailable, "provisioner failed")
	}
	return &pb.ProvisionResponse{ClientId: result.ClientID, ClientUuid: result.ClientUUID, Subject: result.Subject, ClientSecret: result.Secret}, nil
}
func (s *accountRPC) Disable(ctx context.Context, r *pb.DisableRequest) (*pb.DisableResponse, error) {
	if err := provisionerCaller(ctx); err != nil {
		return nil, err
	}
	if err := s.provider.Disable(ctx, r.GatewayId, r.ServiceAccountId, r.ClientUuid); err != nil {
		return nil, status.Error(codes.Unavailable, "disable failed")
	}
	return &pb.DisableResponse{}, nil
}
func (s *accountRPC) Delete(ctx context.Context, r *pb.DeleteRequest) (*pb.DeleteResponse, error) {
	if err := provisionerCaller(ctx); err != nil {
		return nil, err
	}
	if err := s.provider.Delete(ctx, r.GatewayId, r.ServiceAccountId, r.ClientUuid); err != nil {
		return nil, status.Error(codes.Unavailable, "delete failed")
	}
	return &pb.DeleteResponse{}, nil
}
func (s *accountRPC) DeleteManaged(ctx context.Context, r *pb.DeleteManagedRequest) (*pb.DeleteManagedResponse, error) {
	if err := provisionerCaller(ctx); err != nil {
		return nil, err
	}
	if err := s.provider.Delete(ctx, r.GatewayId, r.ServiceAccountId, ""); err != nil {
		return nil, status.Error(codes.Unavailable, "delete failed")
	}
	return &pb.DeleteManagedResponse{}, nil
}
func startAccountProvisioner(t testing.TB, provider *accountProvider, key *rsa.PrivateKey, settings []string) ([]string, string) {
	t.Helper()
	for _, setting := range settings {
		name, value, _ := strings.Cut(setting, "=")
		t.Setenv(name, value)
	}
	verifier, err := auth.NewVerifierFromEnvironment()
	if err != nil {
		t.Fatal(err)
	}
	identity := identity(t, "localhost")
	directory := filepath.Dir(identity.config.CAFile)
	t.Setenv("STEGO_GRPC_ADDR", "127.0.0.1:0")
	t.Setenv("STEGO_GRPC_TLS_CERT", filepath.Join(directory, "server.pem"))
	t.Setenv("STEGO_GRPC_TLS_KEY", filepath.Join(directory, "server-key.pem"))
	runtime, err := transport.New(verifier.Authenticate, func(registrar grpc.ServiceRegistrar) error {
		pb.RegisterOpenShellGatewayServiceAccountProvisionerServiceServer(registrar, &accountRPC{provider: provider})
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- runtime.Run(ctx) }()
	t.Cleanup(func() {
		cancel()
		runtime.Close()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(time.Second):
			t.Error("provisioner did not stop")
		}
	})
	tokenFile := filepath.Join(t.TempDir(), "service-token")
	if err := os.WriteFile(tokenFile, []byte(token(t, key, "api-provisioner")), 0600); err != nil {
		t.Fatal(err)
	}
	return []string{"HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_ADDR=" + runtime.Addr().String(), "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_CA_FILE=" + identity.config.CAFile, "HYPERSHELL_SERVICE_ACCOUNT_PROVISIONER_TOKEN_FILE=" + tokenFile}, tokenFile
}
func TestServiceAccountWorkflowThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	_, gateway := accountService(t, f, provider)
	key, settings := issuer(t)
	rpcSettings, tokenFile := startAccountProvisioner(t, provider, key, settings)
	settings = append(settings, rpcSettings...)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	path := address + "/api/hypershell/v1/gateways/" + gateway.ID + "/service_accounts"
	owner := token(t, key, "alice")
	reference, err := contracts.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	collection := reference.OpenAPI.Paths.Value("/api/hypershell/v1/gateways/{gateway_id}/service_accounts")
	code, data := requestJSON(t, "POST", path, owner, []byte(`{"name":"runtime","role":"openshell-admin"}`))
	if code != 201 {
		t.Fatalf("create: %d %s", code, data)
	}
	var created map[string]any
	if err := json.Unmarshal(data, &created); err != nil {
		t.Fatal(err)
	}
	if err := collection.Post.Responses.Value("201").Value.Content["application/json"].Schema.Value.VisitJSON(created); err != nil {
		t.Fatal(err)
	}
	id := created["id"].(string)
	secret := created["credential"].(map[string]any)["client_secret"].(string)
	for _, target := range []string{path, path + "/" + id} {
		code, data = requestJSON(t, "GET", target, owner, nil)
		if code != 200 || strings.Contains(string(data), secret) || strings.Contains(string(data), "\"client_secret\":") {
			t.Fatalf("later response: %d %s", code, data)
		}
	}
	code, data = requestJSON(t, "GET", path, owner, nil)
	var list any
	if json.Unmarshal(data, &list) != nil {
		t.Fatal("invalid list JSON")
	}
	if err := collection.Get.Responses.Value("200").Value.Content["application/json"].Schema.Value.VisitJSON(list); err != nil {
		t.Fatal(err)
	}
	if code, data := requestJSON(t, "GET", path+"/"+id, token(t, key, "mallory", "platform:admin"), nil); code != 404 {
		t.Fatalf("admin bypassed grant: %d %s", code, data)
	}
	if code, data := requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+gateway.ID, owner, nil); code != 409 {
		t.Fatalf("Gateway deletion bypassed cleanup: %d %s", code, data)
	}
	// A correctly signed token for another subject cannot provision identities.
	if err := os.WriteFile(tokenFile, []byte(token(t, key, "wrong-service")), 0600); err != nil {
		t.Fatal(err)
	}
	code, data = requestJSON(t, "POST", path, owner, []byte(`{"name":"denied-service"}`))
	if code != 503 {
		t.Fatalf("service identity: %d %s", code, data)
	}
	if err := os.WriteFile(tokenFile, []byte(token(t, key, "api-provisioner")), 0600); err != nil {
		t.Fatal(err)
	}
	provider.mu.Lock()
	provider.failChange = true
	provider.mu.Unlock()
	code, data = requestJSON(t, "POST", path+"/"+id+"/revoke", owner, nil)
	if code != 202 {
		t.Fatalf("pending revoke: %d %s", code, data)
	}
	stop()
	provider.mu.Lock()
	provider.failChange = false
	provider.mu.Unlock()
	stop, address = startApplication(t, binary, f.dsn, config, settings...)
	path = address + "/api/hypershell/v1/gateways/" + gateway.ID + "/service_accounts"
	deadline := time.Now().Add(12 * time.Second)
	for {
		var state string
		if err := f.db.QueryRow(`SELECT status FROM service_accounts WHERE id=$1`, id).Scan(&state); err != nil {
			t.Fatal(err)
		}
		if state == "revoked" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("runtime did not recover pending revocation")
		}
		time.Sleep(50 * time.Millisecond)
	}
	provider.mu.Lock()
	disabled := provider.disabled[id]
	provider.mu.Unlock()
	if !disabled {
		t.Fatal("recovery did not disable provider identity")
	}
	code, data = requestJSON(t, "DELETE", path+"/"+id, owner, nil)
	if code != 204 {
		t.Fatalf("delete: %d %s", code, data)
	}
	code, data = requestJSON(t, "GET", path+"/"+id, owner, nil)
	if code != 404 {
		t.Fatalf("deleted account visible: %d %s", code, data)
	}
	// Failed creation is recovered by the same generated background task.
	deadline = time.Now().Add(12 * time.Second)
	for {
		var live int
		if err := f.db.QueryRow(`SELECT count(*) FROM service_accounts WHERE deleted_at IS NULL`).Scan(&live); err != nil {
			t.Fatal(err)
		}
		if live == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("failed creation was not recovered")
		}
		time.Sleep(50 * time.Millisecond)
	}
	code, data = requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+gateway.ID, owner, nil)
	if code != 204 {
		t.Fatalf("Gateway after account cleanup: %d %s", code, data)
	}
	stop()
}
func TestGeneratedProvisionerDescriptorsMatchReference(t *testing.T) {
	reference, err := contracts.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	actual := protodesc.ToFileDescriptorProto(pb.File_hypershell_provisioner_v1_service_accounts_proto)
	expected := protodesc.ToFileDescriptorProto(reference.Proto.FindFileByPath(actual.GetName()))
	actual.Options.GoPackage = nil
	expected.Options.GoPackage = nil
	actual.SourceCodeInfo = nil
	expected.SourceCodeInfo = nil
	if !proto.Equal(actual, expected) {
		t.Fatal("provisioner wire descriptors changed")
	}
}

func (s *accountRPC) Reconcile(ctx context.Context, r *pb.ReconcileRequest) (*pb.ReconcileResponse, error) {
	if err := provisionerCaller(ctx); err != nil {
		return nil, err
	}
	spec := r.GetSpec()
	if spec == nil || !r.Enabled {
		return nil, status.Error(codes.InvalidArgument, "invalid reconciliation")
	}
	if err := s.provider.Reconcile(ctx, serviceaccounts.Spec{ServiceAccountID: spec.ServiceAccountId, Role: spec.Role}, r.ClientUuid, r.ExpectedSubject); err != nil {
		return nil, status.Error(codes.Unavailable, "reconcile failed")
	}
	return &pb.ReconcileResponse{}, nil
}

func BenchmarkServiceAccountLifecycle(b *testing.B) {
	f := database(b)
	provider := newAccountProvider()
	_, gateway := accountService(b, f, provider)
	key, settings := issuer(b)
	rpcSettings, _ := startAccountProvisioner(b, provider, key, settings)
	settings = append(settings, rpcSettings...)
	_, config := broker(b, identity(b, "localhost"))
	stop, address, _ := startBoth(b, buildApplication(b), f.dsn, config, settings...)
	defer stop()
	path := address + "/api/hypershell/v1/gateways/" + gateway.ID + "/service_accounts"
	owner := token(b, key, "alice")
	awaitQueueEmpty(b, f)
	b.ResetTimer()
	for range b.N {
		code, data := requestJSON(b, "POST", path, owner, []byte(`{"name":"benchmark"}`))
		if code != 201 {
			b.Fatalf("create status %d", code)
		}
		var created struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(data, &created) != nil || created.ID == "" {
			b.Fatal("invalid creation result")
		}
		if code, _ := requestJSON(b, "GET", path+"/"+created.ID, owner, nil); code != 200 {
			b.Fatalf("get status %d", code)
		}
		if code, _ := requestJSON(b, "POST", path+"/"+created.ID+"/revoke", owner, nil); code != 200 {
			b.Fatalf("revoke status %d", code)
		}
		if code, _ := requestJSON(b, "DELETE", path+"/"+created.ID, owner, nil); code != 204 {
			b.Fatalf("delete status %d", code)
		}
	}
	b.StopTimer()
}
