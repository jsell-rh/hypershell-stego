package acceptance

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/metadata"
)

type backlogProvider struct{ cluster, failed string }

func (p *backlogProvider) Handles(*pb.Gateway) bool                     { return true }
func (p *backlogProvider) CleanupTarget() string                        { return p.cluster }
func (p *backlogProvider) GatewayIDs(context.Context) ([]string, error) { return nil, nil }
func (p *backlogProvider) Ensure(context.Context, *pb.Gateway, *pb.ManagedDatabase, *pb.GatewayRelease) error {
	return nil
}
func (p *backlogProvider) Delete(ctx context.Context, gw *pb.Gateway) error {
	id := gw.GetMetadata().GetId()
	if id == p.failed {
		return gatewayworkload.ErrPending
	}
	timer := time.NewTimer(5 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func TestGatewayBacklogLargerThanQueueMakesProgress(t *testing.T) {
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
	// Seed through the domain transaction path while the API delivers events.
	// Keep the controller stopped until the queue is empty and the API restarts.
	// The recovery check starts with retained rows after event delivery.
	total := gatewayworkload.QueueCapacity + 256
	ids := make([]string, 0, total)
	owner := principal("backlog-owner", "gateway:creator")
	for i := 0; i < total; i++ {
		row, err := f.service.Create(context.Background(), owner, f.request(fmt.Sprintf("backlog-%04d", i)))
		if err != nil {
			t.Fatal(err)
		}
		if err := f.service.Delete(context.Background(), owner, row.ID); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, row.ID)
	}
	sort.Strings(ids)
	awaitQueueEmptyWithin(t, f, 45*time.Second)
	readEvent(t, consumer, ids[len(ids)-1])
	readGatewayEvent(t, consumer, ids[len(ids)-1], "Delete", "gateway.deleted")
	// Discovery must reconstruct the backlog after the original events are gone.
	stop()
	stop, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	var pending int
	if err := f.db.QueryRow(`SELECT count(*) FROM gateways WHERE deleted_at IS NOT NULL AND stego_cleanup->>'workload'='false'`).Scan(&pending); err != nil || pending != total {
		t.Fatalf("recovery did not start with the full retained backlog: %d of %d, %v", pending, total, err)
	}
	_, connection := grpcClient(t, rpcAddress, apiTLS)
	state := control.NewGatewayIdentityServiceClient(connection)
	ctx, cancel := context.WithTimeout(metadata.NewOutgoingContext(context.Background(), metadata.Pairs("authorization", "Bearer "+token(t, key, "controller"))), 105*time.Second)
	defer cancel()
	provider := &backlogProvider{cluster: f.cluster, failed: ids[0]}
	controller, err := gatewayworkload.New(pb.NewGatewayServiceClient(connection), state, pb.NewManagedDatabaseServiceClient(connection), pb.NewGatewayReleaseServiceClient(connection), provider)
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
			t.Error("controller did not join")
		}
	}()
	started := time.Now()
	deadline := started.Add(90 * time.Second)
	nextProgress := started.Add(10 * time.Second)
	for {
		var completed int
		if err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM gateways WHERE stego_cleanup->>'workload'='true'`).Scan(&completed); err != nil {
			t.Fatal(err)
		}
		if completed == total-1 {
			break
		}
		if time.Now().After(nextProgress) {
			t.Logf("backlog progress after %s: %d of %d healthy Gateways", time.Since(started).Round(time.Millisecond), completed, total-1)
			nextProgress = time.Now().Add(10 * time.Second)
		}
		if time.Now().After(deadline) {
			t.Fatalf("backlog did not progress beyond capacity: %d of %d healthy Gateways completed", completed, total-1)
		}
		time.Sleep(20 * time.Millisecond)
	}
	for _, id := range []string{ids[0], ids[len(ids)-1]} {
		observed, err := state.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: id})
		if err != nil || !observed.GetDeleted() || observed.GetCleanupTargets()["workload"].GetTargets()[f.cluster] != (id != provider.failed) {
			t.Fatal("wrong retained cleanup observation", id, err)
		}
	}
	readGatewayEvent(t, consumer, ids[len(ids)-1], "Delete", "gateway.deleted")
	if code, _ := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways/"+ids[len(ids)-1], token(t, key, "backlog-owner"), nil); code != 404 {
		t.Fatal("cleanup changed public deletion", code)
	}
	t.Logf("%d retained Gateways crossed a %d-key queue in %s; one provider remained pending", total, gatewayworkload.QueueCapacity, time.Since(started).Round(time.Millisecond))
}
