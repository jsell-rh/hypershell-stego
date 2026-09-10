package acceptance

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/metadata"
)

type identityProviderCall struct {
	ctx    context.Context
	name   string
	result chan string
}

type pausedIdentityProvider struct {
	*checkpointProvider
	calls chan identityProviderCall
}

func (p *pausedIdentityProvider) EnsureGateway(ctx context.Context, _ string, name string) (string, error) {
	call := identityProviderCall{ctx: ctx, name: name, result: make(chan string, 1)}
	select {
	case p.calls <- call:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	select {
	case value := <-call.result:
		return value, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (p *pausedIdentityProvider) next(t *testing.T) identityProviderCall {
	t.Helper()
	select {
	case call := <-p.calls:
		return call
	case <-time.After(10 * time.Second):
		t.Fatal("identity provider was not called")
		return identityProviderCall{}
	}
}

func TestIdentityConditionDuringProviderTimeoutAndDesiredChange(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("identity-race"))
	if err != nil {
		t.Fatal(err)
	}
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	settings = withControllerWriteGrants(t, settings, writeGrant("controller", "configure.identity", ""))
	binary := buildApplication(t)
	stopAPI, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stopAPI() }()
	public, connection := grpcClient(t, rpcAddress, apiTLS)
	client := control.NewGatewayIdentityServiceClient(connection)
	auth := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "controller")))
	owner := token(t, key, "alice")
	read := func() *control.GetGatewayIdentityStateResponse {
		t.Helper()
		value, err := observationRead(auth, func(call context.Context) (*control.GetGatewayIdentityStateResponse, error) {
			return client.GetGatewayIdentityState(call, &control.GetGatewayIdentityStateRequest{Id: gateway.ID})
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	condition := func(state *control.GetGatewayIdentityStateResponse) *control.ResourceCondition {
		return state.GetConditions()["identity"].GetConditions()["ClientReady"]
	}
	wait := func(reason string) *control.GetGatewayIdentityStateResponse {
		t.Helper()
		deadline := time.Now().Add(25 * time.Second)
		for {
			state := read()
			if value := condition(state); value != nil && value.Reason == reason {
				return state
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				t.Fatal("identity condition did not change", condition(state))
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	start := func() (*pausedIdentityProvider, func()) {
		t.Helper()
		provider := &pausedIdentityProvider{checkpointProvider: &checkpointProvider{roles: make(map[string][]string)}, calls: make(chan identityProviderCall, 1)}
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
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("identity controller did not stop")
				}
			})
		}
		t.Cleanup(finish)
		return provider, finish
	}
	readGatewayEvent(t, consumer, gateway.ID, "Create", "gateway.created")
	provider, stopController := start()
	provider.next(t).result <- `{"client":"initial"}`
	ready := wait("IdentityClientReady")
	if !condition(ready).Current || condition(ready).Status != "True" {
		t.Fatal("initial client is not ready", ready)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	// The user scan also publishes its separate condition.
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	// The generated watch must trigger another pass even when the client is ready.
	timed := provider.next(t)
	failed := wait("IdentityObservationTimeout")
	if !errors.Is(timed.ctx.Err(), context.DeadlineExceeded) || condition(failed).Status != "Unknown" || !condition(failed).Current || failed.ResourceGeneration != ready.ResourceGeneration || failed.Gateway.GetOidc() != ready.Gateway.GetOidc() {
		t.Fatal("provider timeout left invalid evidence", failed)
	}
	stopController()
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	stopAPI()
	stopAPI, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	public, connection = grpcClient(t, rpcAddress, apiTLS)
	client = control.NewGatewayIdentityServiceClient(connection)
	restarted := read()
	if restarted.ResourceVersion != failed.ResourceVersion || condition(restarted).Reason != "IdentityObservationTimeout" || condition(restarted).LastTransitionTime != condition(failed).LastTransitionTime {
		t.Fatal("restart lost timeout evidence", restarted)
	}
	provider, stopController = start()
	stale := provider.next(t)
	if stale.name != "identity-race" {
		t.Fatal("provider received the wrong desired state", stale.name)
	}
	path := address + "/api/hypershell/v1/gateways/" + gateway.ID
	if code, _ := requestJSON(t, "PATCH", path, owner, []byte(`{"name":"identity-new"}`)); code != 200 {
		t.Fatal("desired-state change failed", code)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	pending := read()
	if condition(pending).Current || condition(pending).Reason != "ObservationPending" {
		t.Fatal("changed state kept a current condition", pending)
	}
	stale.result <- `{"client":"stale"}`
	// The next call is a barrier: the old pass has returned from its attempted
	// conditional write. Keep the new pass paused while the test reads storage.
	fresh := provider.next(t)
	if fresh.name != "identity-new" {
		t.Fatal("retry did not read current desired state", fresh.name)
	}
	afterOldPass := read()
	if afterOldPass.ResourceVersion != pending.ResourceVersion || afterOldPass.Gateway.GetOidc() != ready.Gateway.GetOidc() || condition(afterOldPass).Current {
		t.Fatal("old provider result changed current state", afterOldPass)
	}
	fresh.result <- `{"client":"current"}`
	recovered := wait("IdentityClientReady")
	if !condition(recovered).Current || condition(recovered).ObservedGeneration != recovered.ResourceGeneration || recovered.Gateway.GetOidc() != `{"client":"current"}` {
		t.Fatal("fresh pass did not recover", recovered)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	recovered = read()
	// Parent cancellation cannot turn an unfinished provider call into a failure
	// observation. Stop only after the next provider call has begun.
	canceled := provider.next(t)
	stopController()
	stable := read()
	if !errors.Is(canceled.ctx.Err(), context.Canceled) || stable.ResourceVersion != recovered.ResourceVersion || condition(stable).Reason != "IdentityClientReady" {
		t.Fatal("parent cancellation changed condition evidence", stable)
	}
	t.Log("Provider timeout, event delivery, API restart, concurrent REST change, fresh retry, and parent cancellation passed")
}
