package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/metadata"
)

// SQL effects use a controlled provider here. Real CNPG connection and cleanup
// checks remain a separate required gate.
type sharedCleanupProvider struct {
	cluster, gateway string
	complete         atomic.Bool
	calls            atomic.Int32
}

func (p *sharedCleanupProvider) Handles(gw *pb.Gateway) bool                  { return gw.GetClusterId() == p.cluster }
func (p *sharedCleanupProvider) CleanupTarget() string                        { return p.cluster }
func (p *sharedCleanupProvider) GatewayIDs(context.Context) ([]string, error) { return nil, nil }
func (p *sharedCleanupProvider) Ensure(context.Context, *pb.Gateway, *pb.GatewayRelease) error {
	return nil
}
func (p *sharedCleanupProvider) DeleteDatabase(_ context.Context, gw *pb.Gateway) error {
	if gw.GetMetadata().GetId() != p.gateway || gw.GetClusterId() != p.cluster {
		return errors.New("cleanup did not receive its assigned Gateway")
	}
	p.calls.Add(1)
	if !p.complete.Load() {
		return gatewayworkload.ErrPending
	}
	return nil
}
func (p *sharedCleanupProvider) Delete(ctx context.Context, gw *pb.Gateway) error {
	return p.DeleteDatabase(ctx, gw)
}

func TestGatewaySQLCleanupPreservesOtherGatewayThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	settings = withCleanupGrants(t, settings, cleanupGrant("controller", "Gateway", "workload", f.cluster), cleanupGrant("controller", "Gateway", "sql", f.cluster))
	settings = withControllerWriteGrants(t, settings, writeGrant("controller", "observe.workload", f.cluster))
	binary := buildApplication(t)
	stop, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	bearer := token(t, key, "shared-owner", "gateway:creator")
	create := func(name string) httpapi.Gateway {
		t.Helper()
		input, _ := json.Marshal(f.request(name))
		code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", bearer, input)
		var row httpapi.Gateway
		if code != 201 || json.Unmarshal(body, &row) != nil {
			t.Fatal("local shared Gateway creation failed", code)
		}
		return row
	}
	first, second := create("first-shared"), create("second-shared")
	readEvent(t, consumer, first.ID)
	if code, _ := requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+first.ID, bearer, nil); code != 202 {
		t.Fatal("Gateway deletion failed", code)
	}
	readGatewayEvent(t, consumer, first.ID, "Update", "gateway.updated")
	awaitQueueEmpty(t, f)
	// The worker starts after event delivery and API restart. Retained state must
	// recover the pending work without an old event payload as authority.
	stop()
	stop, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection := grpcClient(t, rpcAddress, apiTLS)
	state := control.NewGatewayIdentityServiceClient(connection)
	ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token(t, key, "controller"))), 45*time.Second)
	defer cancel()
	provider := &sharedCleanupProvider{cluster: f.cluster, gateway: first.ID}
	controller, err := gatewayworkload.New(pb.NewGatewayServiceClient(connection), state, pb.NewGatewayReleaseServiceClient(connection), provider)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- controller.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("controller did not stop")
		}
	}()
	await := func(complete bool) {
		t.Helper()
		for {
			observed, err := observationRead(ctx, func(ctx context.Context) (*control.GetGatewayIdentityStateResponse, error) {
				return state.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: first.ID})
			})
			if err != nil {
				t.Fatal(err)
			}
			if provider.calls.Load() > 0 && observed.GetCleanupTargets()["workload"].GetTargets()[f.cluster] == complete && observed.GetCleanupTargets()["sql"].GetTargets()[f.cluster] == complete {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("cleanup did not reach the required state", complete)
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	await(false)
	provider.complete.Store(true)
	await(true)
	if code, _ := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways/"+second.ID, bearer, nil); code != 200 {
		t.Fatal("cleanup removed the other Gateway", code)
	}
	if code, _ := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways/"+first.ID, bearer, nil); code != 404 {
		t.Fatal("cleanup restored a deleted Gateway", code)
	}
}
