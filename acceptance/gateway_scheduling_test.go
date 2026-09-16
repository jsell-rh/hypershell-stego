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

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type blockedCleanupProvider struct {
	cluster, slow string
	cleanupOwner  string
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
func (p *blockedCleanupProvider) Ensure(context.Context, *pb.Gateway, *pb.GatewayRelease) error {
	return nil
}
func (p *blockedCleanupProvider) Delete(ctx context.Context, gw *pb.Gateway) error {
	if p.cleanupOwner == "sql" {
		return nil
	}
	return p.deleteID(ctx, gw.GetMetadata().GetId())
}
func (p *blockedCleanupProvider) DeleteDatabase(ctx context.Context, gw *pb.Gateway) error {
	if p.cleanupOwner != "sql" {
		return nil
	}
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

func (p *blockedIdentityCleanupProvider) EnsureGateway(context.Context, string, string, int64) (string, error) {
	return "{}", nil
}
func (p *blockedIdentityCleanupProvider) DeleteGateway(ctx context.Context, id string, _ int64) error {
	return p.deleteID(ctx, id)
}
func (p *blockedIdentityCleanupProvider) ReconcileGatewayUser(context.Context, string, string, string, string) error {
	return nil
}

func TestGatewaySQLCleanupMakesIndependentProgressAfterRestart(t *testing.T) {
	testIndependentResourceCleanup(t, "sql")
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
	foreignCluster := ksuid.New().String()
	if err := f.storage.Create(context.Background(), "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: foreignCluster}, Name: "foreign", Provider: "kubernetes", KubeconfigSecret: "foreign-ref"}); err != nil {
		t.Fatal(err)
	}
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	resource := "Gateway"
	endpoint := "gateways"
	target := ""
	if cleanupOwner == "workload" || cleanupOwner == "sql" {
		target = f.cluster
	}
	grants := []auth.Grant{cleanupGrant("controller", resource, cleanupOwner, target)}
	if cleanupOwner != "identity" {
		grants = []auth.Grant{cleanupGrant("controller", "Gateway", "sql", f.cluster), cleanupGrant("controller", "Gateway", "workload", f.cluster)}
	}
	settings = withCleanupGrants(t, settings, grants...)
	binary := buildApplication(t)
	stop, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	owner := token(t, key, "owner", "gateway:creator")
	root := address + "/api/hypershell/v1/" + endpoint
	event := func(id, action, kind string) {
		t.Helper()
		readGatewayEvent(t, consumer, id, action, "gateway."+kind)
	}
	ids := make([]string, 0, 2)
	for _, name := range []string{"blocked-cleanup", "independent-cleanup"} {
		input := map[string]string{"name": name, "cluster_id": f.cluster, "release_id": f.release}
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
		if code, data := requestJSON(t, "DELETE", root+"/"+row.ID, owner, nil); code != 202 {
			t.Fatalf("delete: %d %s", code, data)
		}
		event(row.ID, "Update", "updated")
	}
	foreignID := ""
	if cleanupOwner == "sql" {
		body, _ := json.Marshal(map[string]string{"name": "other-cluster", "cluster_id": foreignCluster, "release_id": f.release})
		code, data := requestJSON(t, "POST", root, owner, body)
		var row struct {
			ID string `json:"id"`
		}
		if code != 201 || json.Unmarshal(data, &row) != nil {
			t.Fatal("create other scope", code)
		}
		foreignID = row.ID
		event(row.ID, "Create", "created")
		if code, _ := requestJSON(t, "DELETE", root+"/"+row.ID, owner, nil); code != 202 {
			t.Fatal("delete other scope", code)
		}
		event(row.ID, "Update", "updated")
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
	if _, err := state.GetGatewayCleanupSummary(ctx, &control.GetGatewayCleanupSummaryRequest{Owner: "sql", Target: foreignCluster}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("summary crossed its cluster grant", err)
	}
	otherOwner, otherTarget := "identity", ""
	if cleanupOwner == "identity" {
		otherOwner, otherTarget = "sql", f.cluster
	}
	if _, err := state.GetGatewayCleanupSummary(ctx, &control.GetGatewayCleanupSummaryRequest{Owner: otherOwner, Target: otherTarget}); status.Code(err) != codes.PermissionDenied {
		t.Fatal("summary crossed its owner grant", err)
	}
	provider := &blockedCleanupProvider{cluster: f.cluster, cleanupOwner: cleanupOwner, slow: ids[0], entered: make(chan struct{}), release: make(chan struct{}), transient: true}
	var controller interface {
		RunWithMetrics(context.Context, *runtime.Metrics) error
	}
	if cleanupOwner == "identity" {
		controller, err = gatewayidentity.New(api, state, &blockedIdentityCleanupProvider{provider})
	} else {
		controller, err = gatewayworkload.New(api, state, pb.NewGatewayReleaseServiceClient(connection), provider)
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
	event(ids[1], "Update", "updated")
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
	event(ids[0], "Update", "updated")
	finishedSummary, err := summaryRead(ctx)
	if err != nil || finishedSummary.GetPending() != 0 || finishedSummary.GetOldestPending() != nil {
		t.Fatal("finished cleanup summary", finishedSummary, err)
	}
	if foreignID != "" {
		row, err := state.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: foreignID})
		if err != nil || !row.GetDeleted() || row.GetCleanupTargets()["sql"].GetTargets()[foreignCluster] || row.GetCleanupTargets()["workload"].GetTargets()[foreignCluster] {
			t.Fatal("cleanup changed another cluster", err)
		}
	}
	if provider.overlap.Load() {
		t.Fatal("one resource had concurrent provider actions")
	}
	for _, id := range ids {
		requireDeletingGateway(t, address+"/api/hypershell/v1/"+endpoint+"/"+id, owner)
	}
	t.Log("REST deletion, retained discovery after API restart, independent cleanup, TLS gRPC observations, and event delivery passed")
}
