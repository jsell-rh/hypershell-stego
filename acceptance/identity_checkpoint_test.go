package acceptance

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// The provider records only successful actions. Real API calls supply all state.
type checkpointProvider struct {
	mu         sync.Mutex
	roles      map[string][]string
	pauseAfter int
}

func (*checkpointProvider) EnsureGateway(context.Context, string, string) (string, error) {
	return "{}", nil
}
func (*checkpointProvider) DeleteGateway(context.Context, string) error  { return nil }
func (*checkpointProvider) GatewayIDs(context.Context) ([]string, error) { return nil, nil }
func (p *checkpointProvider) ReconcileGatewayUser(ctx context.Context, _, _, subject, role string) error {
	p.mu.Lock()
	pause := p.pauseAfter > 0 && len(p.roles) >= p.pauseAfter
	if !pause {
		p.roles[subject] = append(p.roles[subject], role)
	}
	p.mu.Unlock()
	if pause {
		<-ctx.Done()
		return ctx.Err()
	}
	return nil
}

func TestIdentityCheckpointSurvivesAPIAndControllerRestart(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("identity-checkpoint"))
	if err != nil {
		t.Fatal(err)
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	seedDiscoveryGrants(t, f, gateway.ID, input.RoleID, 105)
	_, brokerConfig := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	settings = withControllerWriteGrants(t, settings, writeGrant("controller", "configure.identity", ""))
	binary := buildApplication(t)
	stopAPI, _, address := startBoth(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stopAPI() }()
	public, connection := grpcClient(t, address, apiTLS)
	client := control.NewGatewayIdentityServiceClient(connection)
	auth := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "controller")))
	provider := &checkpointProvider{roles: make(map[string][]string), pauseAfter: 7}
	run := func() func() {
		controller, err := gatewayidentity.New(public, client, provider)
		if err != nil {
			t.Fatal(err)
		}
		work, stop := context.WithCancel(auth)
		done := make(chan error, 1)
		go func() { done <- controller.Run(work) }()
		var once sync.Once
		finish := func() {
			once.Do(func() {
				stop()
				select {
				case err := <-done:
					if err != nil && !errors.Is(err, context.Canceled) {
						t.Error("controller stopped with error", err)
					}
				case <-time.After(5 * time.Second):
					t.Error("controller did not stop")
				}
			})
		}
		t.Cleanup(finish)
		return finish
	}
	waitCheckpoint := func(version int64, after string) *control.GatewayIdentityCycle {
		t.Helper()
		deadline := time.Now().Add(30 * time.Second)
		for {
			value, err := client.LoadGatewayIdentityCycle(auth, &control.LoadGatewayIdentityCheckpointRequest{GatewayId: gateway.ID})
			state, decodeErr := runtime.DecodeCycle(value.GetData())
			if err == nil && decodeErr == nil && value.Version == version && ((after != "" && state.After == after && !state.Complete) || (after == "" && state.Complete)) {
				return value
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				t.Fatal("checkpoint did not reach expected state", value, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	stopController := run()
	waitCheckpoint(1, fmt.Sprintf("%027d", 7))
	stopController()
	// The next action must read the changed grant after both restarts.
	if _, err := f.db.Exec("UPDATE role_bindings SET deleted_at=now() WHERE id=$1", fmt.Sprintf("%027d", 8)); err != nil {
		t.Fatal(err)
	}
	stopAPI()
	stopAPI, _, address = startBoth(t, binary, f.dsn, brokerConfig, settings...)
	public, connection = grpcClient(t, address, apiTLS)
	client = control.NewGatewayIdentityServiceClient(connection)
	waitCheckpoint(1, fmt.Sprintf("%027d", 7))
	provider.mu.Lock()
	provider.pauseAfter = 0
	provider.mu.Unlock()
	stopController = run()
	completed := waitCheckpoint(2, "")
	if cycle, err := runtime.DecodeCycle(completed.Data); err != nil || !cycle.Failed {
		t.Fatal("resumed cycle lost its earlier timeout failure", cycle, err)
	}
	stopController()
	provider.mu.Lock()
	defer provider.mu.Unlock()
	if len(provider.roles) != 106 {
		t.Fatal("controller lost users", len(provider.roles))
	}
	for subject, roles := range provider.roles {
		if len(roles) != 1 {
			t.Fatal("saved prefix was repeated", subject, roles)
		}
	}
	if roles := provider.roles["discovery-8"]; len(roles) != 1 || roles[0] != "" {
		t.Fatal("new controller used stale access", roles)
	}
	if err := f.service.Delete(ctx, principal("alice"), gateway.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := client.SaveGatewayIdentityCycle(auth, &control.SaveGatewayIdentityCycleRequest{GatewayId: gateway.ID, ExpectedVersion: 2, Data: completed.Data, ResourceGeneration: completed.ResourceGeneration}); status.Code(err) != codes.NotFound {
		t.Fatal("deleted Gateway accepted a cursor save", err)
	}
	if _, err := client.LoadGatewayIdentityCycle(auth, &control.LoadGatewayIdentityCheckpointRequest{GatewayId: gateway.ID}); status.Code(err) != codes.NotFound {
		t.Fatal("deleted Gateway allowed another live scan", err)
	}
	var retained int64
	if err := f.db.QueryRow("SELECT version FROM stego_scan_checkpoints WHERE entity='Gateway' AND resource_id=$1 AND scope='identity-users-cycle'", gateway.ID).Scan(&retained); err != nil || retained != 2 {
		t.Fatal("deletion changed checkpoint history", retained, err)
	}
	t.Log("A new controller loaded the durable cursor after API restart and checked current grants before provider work")
}
