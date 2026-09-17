package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type deadlineObservationProvider struct {
	*blockedCleanupProvider
	calls    atomic.Int64
	timedOut chan struct{}
}

func (p *deadlineObservationProvider) Ensure(ctx context.Context, _ *pb.Gateway, _ *pb.GatewayRelease, _ int64) error {
	return p.observe(ctx)
}
func (p *deadlineObservationProvider) Delete(ctx context.Context, _ *pb.Gateway) error {
	if p.cleanupOwner == "sql" {
		return nil
	}
	return p.observe(ctx)
}
func (p *deadlineObservationProvider) DeleteDatabase(ctx context.Context, _ *pb.Gateway) error {
	if p.cleanupOwner != "sql" {
		return nil
	}
	return p.observe(ctx)
}
func (p *deadlineObservationProvider) observe(ctx context.Context) error {
	if p.calls.Add(1) == 1 {
		return nil
	}
	p.once.Do(func() { close(p.entered) })
	select {
	case <-p.release:
		return nil
	case <-ctx.Done():
		if errors.Is(ctx.Err(), context.DeadlineExceeded) && p.timedOut != nil {
			select {
			case p.timedOut <- struct{}{}:
			default:
			}
		}
		return ctx.Err()
	}
}

type deadlineIdentityObservationProvider struct{ *deadlineObservationProvider }

func (p *deadlineIdentityObservationProvider) EnsureGateway(context.Context, string, string, int64) (string, error) {
	return "{}", nil
}
func (p *deadlineIdentityObservationProvider) DeleteGateway(ctx context.Context, _ string, _ int64) error {
	return p.observe(ctx)
}
func (p *deadlineIdentityObservationProvider) ReconcileGatewayUser(context.Context, string, string, string, string) error {
	return nil
}

// A concurrent conditional observation can abort a serializable read. Repeat
// only that documented conflict, with a short limit and a fresh transaction.
func observationRead[T any](ctx context.Context, read func(context.Context) (T, error)) (T, error) {
	bounded, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	for {
		value, err := read(bounded)
		if status.Code(err) != codes.Aborted || bounded.Err() != nil {
			return value, err
		}
		select {
		case <-bounded.Done():
			var zero T
			return zero, bounded.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestGatewayProviderDeadlineCommitsFailureAndRecovers(t *testing.T) {
	testProviderDeadlineObservation(t, "gateway", false)
}
func TestGatewayCleanupDeadlineReopensConfirmation(t *testing.T) {
	testProviderDeadlineObservation(t, "gateway", true)
}
func TestGatewaySQLCleanupDeadlineKeepsStateUntilRecovery(t *testing.T) {
	testProviderDeadlineObservation(t, "sql", true)
}
func TestIdentityCleanupDeadlineReopensConfirmation(t *testing.T) {
	testProviderDeadlineObservation(t, "identity", true)
}
func testProviderDeadlineObservation(t *testing.T, resource string, cleanup bool) {
	sqlResource, identityResource := resource == "sql", resource == "identity"
	f := database(t)
	t.Cleanup(func() {
		if t.Failed() {
			logQueueState(t, f)
		}
	})
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	settings = withControllerWriteGrants(t, settings, writeGrant("controller", "observe.workload", f.cluster))
	if cleanup {
		if identityResource {
			settings = withCleanupGrants(t, settings, cleanupGrant("controller", "Gateway", "identity", ""))
		} else {
			settings = withCleanupGrants(t, settings, cleanupGrant("controller", "Gateway", "sql", f.cluster), cleanupGrant("controller", "Gateway", "workload", f.cluster))
		}
	}
	binary := buildApplication(t)
	stopAPI, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stopAPI() }()
	owner := token(t, key, "alice", "gateway:creator")
	body, _ := json.Marshal(f.request("deadline-observation"))
	path, healthyPhase, healthyStatus, failedPhase, failedStatus := "/api/hypershell/v1/gateways", "Running", "Healthy", "Degraded", "WorkloadUnavailable"
	action, kind := "Update", "updated"
	if cleanup {
		healthyPhase, healthyStatus, failedPhase, failedStatus = "", "complete", "", "pending"
	}
	type observedResource struct {
		ID            string
		Phase, Status *string
	}
	code, data := requestJSON(t, "POST", address+path, owner, body)
	var created observedResource
	if code != 201 || json.Unmarshal(data, &created) != nil {
		t.Fatal("create resource", code)
	}
	eventObservation := 0
	event := func(action, kind string) {
		t.Helper()
		eventObservation++
		wasFailed := t.Failed()
		defer func() {
			if !wasFailed && t.Failed() {
				// Capture the queue before the enclosing test stops the API.
				// The failure and its ten-second limit remain unchanged.
				t.Logf("event observation=%d queue before API shutdown", eventObservation)
				logQueueState(t, f)
			}
		}()
		readGatewayEvent(t, consumer, created.ID, action, "gateway."+kind)
	}
	event("Create", "created")
	if cleanup {
		if code, _ := requestJSON(t, "DELETE", address+path+"/"+created.ID, owner, nil); code != 202 {
			t.Fatal("delete resource", code)
		}
		event("Update", "updated")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	auth := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "controller")))
	_, observationConnection := grpcClient(t, rpcAddress, apiTLS)
	defer func() { observationConnection.Close() }()
	provider := &deadlineObservationProvider{blockedCleanupProvider: &blockedCleanupProvider{cluster: f.cluster, entered: make(chan struct{}), release: make(chan struct{})}, timedOut: make(chan struct{}, 1)}
	if sqlResource {
		provider.cleanupOwner = "sql"
		provider.calls.Store(1)
	}
	startController := func() func() {
		t.Helper()
		_, connection := grpcClient(t, rpcAddress, apiTLS)
		var controller interface{ Run(context.Context) error }
		var err error
		if identityResource {
			controller, err = gatewayidentity.New(pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), &deadlineIdentityObservationProvider{provider})
		} else {
			controller, err = gatewayworkload.New(pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), pb.NewGatewayReleaseServiceClient(connection), provider)
		}
		if err != nil {
			t.Fatal(err)
		}
		run, stop := context.WithCancel(auth)
		done := make(chan error, 1)
		go func() { done <- controller.Run(run) }()
		return func() {
			stop()
			select {
			case err := <-done:
				if err != nil {
					t.Error("controller shutdown", err)
				}
			case <-time.After(3 * time.Second):
				t.Error("controller did not join")
			}
			connection.Close()
		}
	}
	stopController := startController()
	defer func() {
		if stopController != nil {
			stopController()
		}
	}()
	read := func() observedResource {
		t.Helper()
		if cleanup {
			complete := false
			state, err := observationRead(auth, func(ctx context.Context) (*control.GetGatewayIdentityStateResponse, error) {
				return control.NewGatewayIdentityServiceClient(observationConnection).GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: created.ID})
			})
			if err != nil || !state.GetDeleted() {
				t.Fatal("retained Gateway", err)
			}
			if identityResource {
				complete = state.GetCleanup()["identity"]
			} else if sqlResource {
				complete = state.GetCleanupTargets()["sql"].GetTargets()[f.cluster]
			} else {
				complete = state.GetCleanupTargets()["workload"].GetTargets()[f.cluster]
			}
			status := "pending"
			if complete {
				status = "complete"
			}
			return observedResource{ID: created.ID, Status: &status}
		}
		code, data := requestJSON(t, "GET", address+path+"/"+created.ID, owner, nil)
		deadline := time.Now().Add(2 * time.Second)
		for code == 409 && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
			code, data = requestJSON(t, "GET", address+path+"/"+created.ID, owner, nil)
		}
		var row observedResource
		if code != 200 || json.Unmarshal(data, &row) != nil {
			t.Fatal("resource read", code)
		}
		return row
	}
	await := func(phase, status string, limit time.Duration) {
		t.Helper()
		deadline := time.Now().Add(limit)
		for {
			row := read()
			if row.Status != nil && *row.Status == status && (phase == "" || row.Phase != nil && *row.Phase == phase) {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("resource did not publish %s / %s", phase, status)
			}
			time.Sleep(50 * time.Millisecond)
		}
	}
	if !sqlResource {
		await(healthyPhase, healthyStatus, 8*time.Second)
		event(action, kind)
	}
	select {
	case <-provider.entered:
	case <-time.After(12 * time.Second):
		t.Fatal("provider did not start the blocked observation")
	}
	if sqlResource {
		select {
		case <-provider.timedOut:
		case <-time.After(22 * time.Second):
			t.Fatal("SQL cleanup did not honor its deadline")
		}
	}
	await(failedPhase, failedStatus, 22*time.Second)
	if !sqlResource {
		event(action, kind)
	}
	stopController()
	stopController = nil
	stopAPI()
	stopAPI, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	observationConnection.Close()
	_, observationConnection = grpcClient(t, rpcAddress, apiTLS)
	row := read()
	if row.Status == nil || *row.Status != failedStatus || (failedPhase != "" && (row.Phase == nil || *row.Phase != failedPhase)) {
		t.Fatal("failure observation did not survive API restart")
	}
	close(provider.release)
	stopController = startController()
	await(healthyPhase, healthyStatus, 8*time.Second)
	event(action, kind)
	if cleanup {
		if code, _ := requestJSON(t, "GET", address+path+"/"+created.ID, owner, nil); code != 200 {
			t.Fatal("unfinished cleanup lost public visibility", code)
		}
		return
	}
	_, connection := grpcClient(t, rpcAddress, apiTLS)
	defer connection.Close()
	state, err := control.NewGatewayIdentityServiceClient(connection).GetGatewayIdentityState(auth, &control.GetGatewayIdentityStateRequest{Id: created.ID})
	if err != nil || state.GetObservedGeneration() != state.GetResourceGeneration() {
		t.Fatal("recovery did not confirm the current generation", err)
	}
}
