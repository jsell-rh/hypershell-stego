package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/metadata"
)

type blockedCleanupProvider struct {
	cluster, slow string
	entered       chan struct{}
	release       chan struct{}
	once          sync.Once
	active        atomic.Int32
	overlap       atomic.Bool
}

func (p *blockedCleanupProvider) Handles(*pb.Gateway) bool                     { return true }
func (p *blockedCleanupProvider) CleanupTarget() string                        { return p.cluster }
func (p *blockedCleanupProvider) GatewayIDs(context.Context) ([]string, error) { return nil, nil }
func (p *blockedCleanupProvider) Ensure(context.Context, *pb.Gateway, *pb.ManagedDatabase, *pb.GatewayRelease) error {
	return nil
}
func (p *blockedCleanupProvider) Delete(ctx context.Context, id string) error {
	if id != p.slow {
		return nil
	}
	if p.active.Add(1) != 1 {
		p.overlap.Store(true)
	}
	defer p.active.Add(-1)
	p.once.Do(func() { close(p.entered) })
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestGatewayCleanupMakesIndependentProgressAfterRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	settings = withCleanupGrants(t, settings, cleanupGrant("controller", "Gateway", "workload", f.cluster))
	binary := buildApplication(t)
	stop, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	owner := token(t, key, "owner", "gateway:creator")
	root := address + "/api/hypershell/v1/gateways"
	ids := make([]string, 0, 2)
	for _, name := range []string{"blocked-cleanup", "independent-cleanup"} {
		body, _ := json.Marshal(map[string]string{"name": name, "cluster_id": f.cluster, "release_id": f.release, "database_id": f.database})
		code, data := requestJSON(t, "POST", root, owner, body)
		var row httpapi.Gateway
		if code != 201 || json.Unmarshal(data, &row) != nil {
			t.Fatalf("create: %d %s", code, data)
		}
		ids = append(ids, row.ID)
		readEvent(t, consumer, row.ID)
		if code, data := requestJSON(t, "DELETE", root+"/"+row.ID, owner, nil); code != 204 {
			t.Fatalf("delete: %d %s", code, data)
		}
		readGatewayEvent(t, consumer, row.ID, "Delete", "gateway.deleted")
	}
	sort.Strings(ids)
	awaitQueueEmpty(t, f)
	stop()
	stop, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection := grpcClient(t, rpcAddress, apiTLS)
	api := pb.NewGatewayServiceClient(connection)
	state := control.NewGatewayIdentityServiceClient(connection)
	ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token(t, key, "controller"))), 30*time.Second)
	defer cancel()
	provider := &blockedCleanupProvider{cluster: f.cluster, slow: ids[0], entered: make(chan struct{}), release: make(chan struct{})}
	controller, err := gatewayworkload.New(api, state, pb.NewManagedDatabaseServiceClient(connection), pb.NewGatewayReleaseServiceClient(connection), provider)
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
				t.Error("controller shutdown", err)
			}
		case <-time.After(3 * time.Second):
			t.Error("controller did not join")
		}
	}()
	select {
	case <-provider.entered:
	case <-ctx.Done():
		t.Fatal("retained scan did not start cleanup")
	}
	read := func(id string) *control.GetGatewayIdentityStateResponse {
		t.Helper()
		request, stop := context.WithTimeout(ctx, time.Second)
		defer stop()
		result, err := state.GetGatewayIdentityState(request, &control.GetGatewayIdentityStateRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	awaitComplete := func(id string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			current := read(id)
			if current.GetDeleted() && current.GetCleanupTargets()["workload"].GetTargets()[f.cluster] {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("one blocked Gateway prevented independent cleanup", id)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	awaitComplete(ids[1])
	if read(ids[0]).GetCleanup()["workload"] {
		t.Fatal("blocked provider recorded completion")
	}
	readGatewayEvent(t, consumer, ids[1], "Delete", "gateway.deleted")
	close(provider.release)
	awaitComplete(ids[0])
	readGatewayEvent(t, consumer, ids[0], "Delete", "gateway.deleted")
	if provider.overlap.Load() {
		t.Fatal("one Gateway had concurrent provider actions")
	}
	for _, id := range ids {
		if code, _ := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways/"+id, owner, nil); code != 404 {
			t.Fatal("cleanup changed public deletion", code)
		}
	}
	t.Log("REST deletion, retained discovery after API restart, independent cleanup, TLS gRPC observations, and event delivery passed")
}
