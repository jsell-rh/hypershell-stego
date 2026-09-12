package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type deadlineObservationProvider struct {
	*blockedCleanupProvider
	calls atomic.Int64
}

func (p *deadlineObservationProvider) Ensure(ctx context.Context, _ *pb.Gateway, _ *pb.ManagedDatabase, _ *pb.GatewayRelease) error {
	return p.observe(ctx)
}
func (p *deadlineObservationProvider) Delete(ctx context.Context, _ *pb.Gateway) error {
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
		return ctx.Err()
	}
}

type deadlineDatabaseObservationProvider struct{ *deadlineObservationProvider }

func (p *deadlineDatabaseObservationProvider) Ensure(ctx context.Context, _ *pb.ManagedDatabase) error {
	return p.observe(ctx)
}
func (p *deadlineDatabaseObservationProvider) Delete(ctx context.Context, row *pb.ManagedDatabase) error {
	return p.observe(ctx)
}

type deadlineIdentityObservationProvider struct{ *deadlineObservationProvider }

func (p *deadlineIdentityObservationProvider) EnsureGateway(context.Context, string, string) (string, error) {
	return "{}", nil
}
func (p *deadlineIdentityObservationProvider) DeleteGateway(ctx context.Context, _ string) error {
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
func TestDatabaseProviderDeadlineCommitsFailureAndRecovers(t *testing.T) {
	testProviderDeadlineObservation(t, "database", false)
}
func TestGatewayCleanupDeadlineReopensConfirmation(t *testing.T) {
	testProviderDeadlineObservation(t, "gateway", true)
}
func TestDatabaseCleanupDeadlineReopensConfirmation(t *testing.T) {
	testProviderDeadlineObservation(t, "database", true)
}
func TestIdentityCleanupDeadlineReopensConfirmation(t *testing.T) {
	testProviderDeadlineObservation(t, "identity", true)
}
func testProviderDeadlineObservation(t *testing.T, resource string, cleanup bool) {
	databaseResource, identityResource := resource == "database", resource == "identity"
	f := database(t)
	assignTestDatabaseCluster(t, f)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	if databaseResource {
		settings = withControllerWriteGrants(t, settings, databaseWriteGrant("controller", f.cluster))
	} else {
		settings = withControllerWriteGrants(t, settings, writeGrant("controller", "observe.workload", f.cluster))
	}
	if cleanup {
		if databaseResource {
			settings = withCleanupGrants(t, settings, cleanupGrant("controller", "ManagedDatabase", "provider", f.cluster))
		} else if identityResource {
			settings = withCleanupGrants(t, settings, cleanupGrant("controller", "Gateway", "identity", ""))
		} else {
			settings = withCleanupGrants(t, settings, cleanupGrant("controller", "Gateway", "workload", f.cluster), cleanupGrant("controller", "ManagedDatabase", "record", f.cluster))
		}
	}
	binary := buildApplication(t)
	stopAPI, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stopAPI() }()
	owner := token(t, key, "alice", "gateway:creator")
	body, _ := json.Marshal(f.request("deadline-observation"))
	path, healthyPhase, healthyStatus, failedPhase, failedStatus := "/api/hypershell/v1/gateways", "Running", "Healthy", "Degraded", "WorkloadUnavailable"
	if databaseResource {
		path, healthyPhase, healthyStatus, failedPhase, failedStatus = "/api/hypershell/v1/managed_databases", "", "ready", "", "error"
		owner = token(t, key, "alice", "platform:admin")
		body, _ = json.Marshal(map[string]string{"name": "deadline-observation", "provider": "deployment"})
	}
	action, kind := "Update", "updated"
	if cleanup {
		healthyPhase, healthyStatus, failedPhase, failedStatus = "", "complete", "", "pending"
		action, kind = "Delete", "deleted"
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
	event := func(action, kind string) {
		t.Helper()
		if databaseResource {
			readCatalogEvent(t, consumer, created.ID, "ManagedDatabases", action, "manageddatabase."+kind)
		} else {
			readGatewayEvent(t, consumer, created.ID, action, "gateway."+kind)
		}
	}
	event("Create", "created")
	if cleanup {
		if code, _ := requestJSON(t, "DELETE", address+path+"/"+created.ID, owner, nil); code != 204 {
			t.Fatal("delete resource", code)
		}
		event("Delete", "deleted")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	auth := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "controller")))
	_, observationConnection := grpcClient(t, rpcAddress, apiTLS)
	defer func() { observationConnection.Close() }()
	provider := &deadlineObservationProvider{blockedCleanupProvider: &blockedCleanupProvider{cluster: f.cluster, entered: make(chan struct{}), release: make(chan struct{})}}
	startController := func() func() {
		t.Helper()
		_, connection := grpcClient(t, rpcAddress, apiTLS)
		var controller interface{ Run(context.Context) error }
		var err error
		if databaseResource {
			controller, err = databasecontroller.New(pb.NewManagedDatabaseServiceClient(connection), control.NewDatabaseCleanupServiceClient(connection), f.cluster, &deadlineDatabaseObservationProvider{provider})
		} else if identityResource {
			controller, err = gatewayidentity.New(pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), &deadlineIdentityObservationProvider{provider})
		} else {
			controller, err = gatewayworkload.New(pb.NewGatewayServiceClient(connection), control.NewGatewayIdentityServiceClient(connection), pb.NewManagedDatabaseServiceClient(connection), pb.NewGatewayReleaseServiceClient(connection), provider)
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
			if databaseResource {
				readContext, err := rpc.WithRetainedResourceRead(auth)
				if err != nil {
					t.Fatal(err)
				}
				var header metadata.MD
				response, err := observationRead(readContext, func(ctx context.Context) (*pb.GetManagedDatabaseResponse, error) {
					return pb.NewManagedDatabaseServiceClient(observationConnection).GetManagedDatabase(ctx, &pb.GetManagedDatabaseRequest{Id: created.ID}, grpc.Header(&header))
				})
				if err != nil || response.GetManagedDatabase().GetMetadata().GetId() != created.ID {
					t.Fatal("retained database", err)
				}
				_, deleted, err := rpc.ObservedResourceState(header)
				if err != nil || !deleted {
					t.Fatal("missing database deletion", err)
				}
				states, err := rpc.ObservedCleanupObservations(header)
				if err != nil {
					t.Fatal(err)
				}
				complete = states["provider"]
			} else {
				state, err := observationRead(auth, func(ctx context.Context) (*control.GetGatewayIdentityStateResponse, error) {
					return control.NewGatewayIdentityServiceClient(observationConnection).GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: created.ID})
				})
				if err != nil || !state.GetDeleted() {
					t.Fatal("retained Gateway", err)
				}
				if identityResource {
					complete = state.GetCleanup()["identity"]
				} else {
					complete = state.GetCleanupTargets()["workload"].GetTargets()[f.cluster]
				}
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
	await(healthyPhase, healthyStatus, 8*time.Second)
	event(action, kind)
	select {
	case <-provider.entered:
	case <-time.After(12 * time.Second):
		t.Fatal("provider did not start the blocked observation")
	}
	await(failedPhase, failedStatus, 22*time.Second)
	event(action, kind)
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
		if code, _ := requestJSON(t, "GET", address+path+"/"+created.ID, owner, nil); code != 404 {
			t.Fatal("cleanup changed public deletion", code)
		}
		return
	}
	_, connection := grpcClient(t, rpcAddress, apiTLS)
	defer connection.Close()
	if databaseResource {
		readContext, err := rpc.WithRetainedResourceRead(auth)
		if err != nil {
			t.Fatal(err)
		}
		var header metadata.MD
		_, err = pb.NewManagedDatabaseServiceClient(connection).GetManagedDatabase(readContext, &pb.GetManagedDatabaseRequest{Id: created.ID}, grpc.Header(&header))
		if err != nil {
			t.Fatal(err)
		}
		version, deleted, err := rpc.ObservedResourceState(header)
		if err != nil || deleted || version < 4 {
			t.Fatal("database recovery lost observation revisions", version, err)
		}
		return
	}
	state, err := control.NewGatewayIdentityServiceClient(connection).GetGatewayIdentityState(auth, &control.GetGatewayIdentityStateRequest{Id: created.ID})
	if err != nil || state.GetObservedGeneration() != state.GetResourceGeneration() {
		t.Fatal("recovery did not confirm the current generation", err)
	}
}
