package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type blockedCleanupProvider struct {
	cluster, slow string
	entered       chan struct{}
	release       chan struct{}
	once          sync.Once
	active        atomic.Int32
	overlap       atomic.Bool
	transient     bool
	attempts      atomic.Int32
}

func (p *blockedCleanupProvider) Handles(*pb.Gateway) bool                     { return true }
func (p *blockedCleanupProvider) CleanupTarget() string                        { return p.cluster }
func (p *blockedCleanupProvider) GatewayIDs(context.Context) ([]string, error) { return nil, nil }
func (p *blockedCleanupProvider) Ensure(context.Context, *pb.Gateway, *pb.ManagedDatabase, *pb.GatewayRelease) error {
	return nil
}
func (p *blockedCleanupProvider) Delete(ctx context.Context, gw *pb.Gateway) error {
	return p.deleteID(ctx, gw.GetMetadata().GetId())
}
func (p *blockedCleanupProvider) deleteID(ctx context.Context, id string) error {
	if id != p.slow {
		return nil
	}
	if p.transient && p.attempts.Add(1) == 1 {
		return errors.New("PRIVATE-provider-error")
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

type blockedIdentityCleanupProvider struct{ *blockedCleanupProvider }

func (p *blockedIdentityCleanupProvider) EnsureGateway(context.Context, string, string) (string, error) {
	return "{}", nil
}
func (p *blockedIdentityCleanupProvider) DeleteGateway(ctx context.Context, id string) error {
	return p.deleteID(ctx, id)
}
func (p *blockedIdentityCleanupProvider) ReconcileGatewayUser(context.Context, string, string, string, string) error {
	return nil
}

type blockedDatabaseCleanupProvider struct{ *blockedCleanupProvider }

func (p *blockedDatabaseCleanupProvider) Ensure(context.Context, *pb.ManagedDatabase) error {
	return nil
}
func (p *blockedDatabaseCleanupProvider) Delete(ctx context.Context, row *pb.ManagedDatabase) error {
	return p.blockedCleanupProvider.deleteID(ctx, row.GetMetadata().GetId())
}

func TestDatabaseCleanupMakesIndependentProgressAfterRestart(t *testing.T) {
	testIndependentResourceCleanup(t, "provider")
}
func TestGatewayCleanupMakesIndependentProgressAfterRestart(t *testing.T) {
	testIndependentResourceCleanup(t, "workload")
}
func TestGatewayIdentityCleanupMakesIndependentProgressAfterRestart(t *testing.T) {
	testIndependentResourceCleanup(t, "identity")
}
func testIndependentResourceCleanup(t *testing.T, cleanupOwner string) {
	t.Helper()
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	resource := "Gateway"
	endpoint := "gateways"
	if cleanupOwner == "provider" {
		resource = "ManagedDatabase"
		endpoint = "managed_databases"
	}
	target := ""
	if cleanupOwner == "workload" {
		target = f.cluster
	}
	settings = withCleanupGrants(t, settings, cleanupGrant("controller", resource, cleanupOwner, target))
	binary := buildApplication(t)
	stop, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	owner := token(t, key, "owner", "gateway:creator")
	if cleanupOwner == "provider" {
		owner = token(t, key, "owner", "platform:admin")
	}
	root := address + "/api/hypershell/v1/" + endpoint
	event := func(id, action, kind string) {
		t.Helper()
		if cleanupOwner == "provider" {
			readCatalogEvent(t, consumer, id, "ManagedDatabases", action, "manageddatabase."+kind)
		} else {
			readGatewayEvent(t, consumer, id, action, "gateway."+kind)
		}
	}
	ids := make([]string, 0, 2)
	for _, name := range []string{"blocked-cleanup", "independent-cleanup"} {
		input := map[string]string{"name": name, "cluster_id": f.cluster, "release_id": f.release, "database_id": f.database}
		if cleanupOwner == "provider" {
			input = map[string]string{"name": name, "provider": "deployment"}
		}
		body, _ := json.Marshal(input)
		code, data := requestJSON(t, "POST", root, owner, body)
		var row struct {
			ID string `json:"id"`
		}
		if code != 201 || json.Unmarshal(data, &row) != nil {
			t.Fatalf("create: %d %s", code, data)
		}
		ids = append(ids, row.ID)
		event(row.ID, "Create", "created")
		if code, data := requestJSON(t, "DELETE", root+"/"+row.ID, owner, nil); code != 204 {
			t.Fatalf("delete: %d %s", code, data)
		}
		event(row.ID, "Delete", "deleted")
	}
	if cleanupOwner == "provider" {
		code, data := requestJSON(t, "POST", root, owner, []byte(`{"name":"separate-provider","provider":"cnpg"}`))
		var row struct {
			ID string `json:"id"`
		}
		if code != 201 || json.Unmarshal(data, &row) != nil {
			t.Fatal("create other provider", code, string(data))
		}
		event(row.ID, "Create", "created")
		if code, _ := requestJSON(t, "DELETE", root+"/"+row.ID, owner, nil); code != 204 {
			t.Fatal("delete other provider", code)
		}
		event(row.ID, "Delete", "deleted")
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
	summaryRead := func(callContext context.Context) (*control.CleanupSummary, error) {
		if cleanupOwner == "provider" {
			return control.NewDatabaseCleanupServiceClient(connection).GetDatabaseCleanupSummary(callContext, &control.GetDatabaseCleanupSummaryRequest{Owner: "provider", Provider: "deployment"})
		}
		return state.GetGatewayCleanupSummary(callContext, &control.GetGatewayCleanupSummaryRequest{Owner: cleanupOwner, Target: target})
	}
	initialSummary, err := summaryRead(ctx)
	if err != nil || initialSummary.GetPending() != 2 || initialSummary.GetOldestPending() == nil || initialSummary.GetObservedAt() == nil {
		t.Fatal("retained cleanup summary after restart", initialSummary, err)
	}
	deniedContext := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+owner))
	if _, err := summaryRead(deniedContext); status.Code(err) != codes.PermissionDenied {
		t.Fatal("ordinary caller read cleanup summary", err)
	}
	if cleanupOwner == "provider" {
		other, err := control.NewDatabaseCleanupServiceClient(connection).GetDatabaseCleanupSummary(ctx, &control.GetDatabaseCleanupSummaryRequest{Owner: "provider", Provider: "cnpg"})
		if err != nil || other.GetPending() != 1 {
			t.Fatal("provider summary lost its scope", other, err)
		}
	} else {
		otherOwner, otherTarget := "workload", f.cluster
		if cleanupOwner == "workload" {
			otherOwner, otherTarget = "identity", ""
		}
		if _, err := state.GetGatewayCleanupSummary(ctx, &control.GetGatewayCleanupSummaryRequest{Owner: otherOwner, Target: otherTarget}); status.Code(err) != codes.PermissionDenied {
			t.Fatal("summary bypassed owner grant", err)
		}
	}
	provider := &blockedCleanupProvider{cluster: f.cluster, slow: ids[0], entered: make(chan struct{}), release: make(chan struct{}), transient: true}
	var controller interface {
		RunWithMetrics(context.Context, *runtime.Metrics) error
	}
	if cleanupOwner == "provider" {
		controller, err = databasecontroller.New(pb.NewManagedDatabaseServiceClient(connection), control.NewDatabaseCleanupServiceClient(connection), &blockedDatabaseCleanupProvider{provider})
	} else if cleanupOwner == "identity" {
		controller, err = gatewayidentity.New(api, state, &blockedIdentityCleanupProvider{provider})
	} else {
		controller, err = gatewayworkload.New(api, state, pb.NewManagedDatabaseServiceClient(connection), pb.NewGatewayReleaseServiceClient(connection), provider)
	}
	if err != nil {
		t.Fatal(err)
	}
	metrics := new(runtime.Metrics)
	metricsServer := httptest.NewServer(metrics)
	defer metricsServer.Close()
	done := make(chan error, 1)
	go func() { done <- controller.RunWithMetrics(ctx, metrics) }()
	defer func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error("controller shutdown", err)
			}
			if state := metrics.Snapshot(); state.Running || state.Queue != (runtime.QueueMetrics{}) {
				t.Error("controller retained queue metrics after shutdown", state)
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
	read := func(id string) (bool, bool) {
		t.Helper()
		request, stop := context.WithTimeout(ctx, time.Second)
		defer stop()
		if cleanupOwner == "provider" {
			request, err := rpc.WithRetainedResourceRead(request)
			if err != nil {
				t.Fatal(err)
			}
			var header metadata.MD
			response, err := pb.NewManagedDatabaseServiceClient(connection).GetManagedDatabase(request, &pb.GetManagedDatabaseRequest{Id: id}, grpc.Header(&header))
			if err != nil || response.GetManagedDatabase().GetMetadata().GetId() != id {
				t.Fatal("retained database read", err)
			}
			_, deleted, err := rpc.ObservedResourceState(header)
			if err != nil {
				t.Fatal(err)
			}
			observations, err := rpc.ObservedCleanupObservations(header)
			if err != nil {
				t.Fatal(err)
			}
			return deleted, observations[cleanupOwner]
		}
		result, err := state.GetGatewayIdentityState(request, &control.GetGatewayIdentityStateRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		return result.GetDeleted(), result.GetCleanup()[cleanupOwner]
	}
	awaitComplete := func(id string) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			deleted, complete := read(id)
			if deleted && complete {
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("one blocked resource prevented independent cleanup", id)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	awaitComplete(ids[1])
	if _, complete := read(ids[0]); complete {
		t.Fatal("blocked provider recorded completion")
	}
	event(ids[1], "Delete", "deleted")
	metricsDeadline := time.Now().Add(time.Second)
	for {
		snapshot := metrics.Snapshot()
		if snapshot.Running && snapshot.Queue.Active >= 1 && snapshot.Failed >= 1 && snapshot.Retries >= 1 && snapshot.Succeeded >= 1 && snapshot.CleanupEnabled && snapshot.CleanupAvailable && snapshot.CleanupPending >= 1 && !snapshot.CleanupOldest.IsZero() {
			break
		}
		if time.Now().After(metricsDeadline) {
			t.Fatal("controller metrics lost blocked work or retry", snapshot)
		}
		time.Sleep(time.Millisecond)
	}
	metricsClient := http.Client{Timeout: time.Second}
	response, err := metricsClient.Get(metricsServer.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	data, readErr := io.ReadAll(io.LimitReader(response.Body, 16384))
	response.Body.Close()
	if readErr != nil || response.StatusCode != 200 {
		t.Fatal("controller metrics response", readErr, response.StatusCode)
	}
	for _, metric := range []string{"stego_controller_running", "stego_controller_active_keys", "stego_controller_retries_total", "stego_controller_cleanup_enabled", "stego_controller_cleanup_available", "stego_controller_cleanup_pending_resources", `stego_controller_actions_total{outcome="failure"}`, `stego_controller_actions_total{outcome="success"}`} {
		found := false
		for _, line := range strings.Split(string(data), "\n") {
			if raw, ok := strings.CutPrefix(line, metric+" "); ok {
				value, err := strconv.ParseUint(raw, 10, 64)
				if err != nil || value < 1 {
					t.Fatal("HTTP metrics did not report completed and blocked work", metric, raw)
				}
				found = true
			}
		}
		if !found {
			t.Fatal("HTTP metric missing", metric)
		}
	}
	for _, private := range []string{ids[0], ids[1], "PRIVATE-provider-error", owner} {
		if strings.Contains(string(data), private) {
			t.Fatal("metrics exposed application data")
		}
	}
	pendingSummary, err := summaryRead(ctx)
	if err != nil || pendingSummary.GetPending() != 1 || pendingSummary.GetOldestPending() == nil {
		t.Fatal("completed cleanup stayed pending in summary", pendingSummary, err)
	}
	close(provider.release)
	awaitComplete(ids[0])
	event(ids[0], "Delete", "deleted")
	finishedSummary, err := summaryRead(ctx)
	if err != nil || finishedSummary.GetPending() != 0 || finishedSummary.GetOldestPending() != nil {
		t.Fatal("finished cleanup summary", finishedSummary, err)
	}
	if provider.overlap.Load() {
		t.Fatal("one resource had concurrent provider actions")
	}
	for _, id := range ids {
		if code, _ := requestJSON(t, "GET", address+"/api/hypershell/v1/"+endpoint+"/"+id, owner, nil); code != 404 {
			t.Fatal("cleanup changed public deletion", code)
		}
	}
	t.Log("REST deletion, retained discovery after API restart, independent cleanup, TLS gRPC observations, and event delivery passed")
}
