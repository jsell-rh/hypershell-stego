package acceptance

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

type failedIdentityProvider struct {
	*checkpointProvider
	ready atomic.Bool
}

func (p *failedIdentityProvider) EnsureGateway(context.Context, string, string) (string, error) {
	if p.ready.Load() {
		return "{}", nil
	}
	return "", errors.New("private provider credential=do-not-publish")
}

func TestIdentityProviderFailureHasDurableCondition(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("identity-condition"))
	if err != nil {
		t.Fatal(err)
	}
	_, brokerConfig := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller","observer"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	settings = withControllerWriteGrants(t, settings, writeGrant("controller", "configure.identity", ""))
	binary := buildApplication(t)
	stopAPI, _, address := startBoth(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stopAPI() }()
	public, connection := grpcClient(t, address, apiTLS)
	client := control.NewGatewayIdentityServiceClient(connection)
	auth := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "controller")))
	provider := &failedIdentityProvider{checkpointProvider: &checkpointProvider{roles: make(map[string][]string)}}
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
						t.Error(err)
					}
				case <-time.After(5 * time.Second):
					t.Error("identity controller did not stop")
				}
			})
		}
		t.Cleanup(finish)
		return finish
	}
	read := func() *control.GetGatewayIdentityStateResponse {
		value, err := observationRead(auth, func(ctx context.Context) (*control.GetGatewayIdentityStateResponse, error) {
			return client.GetGatewayIdentityState(ctx, &control.GetGatewayIdentityStateRequest{Id: gateway.ID})
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	condition := func(state *control.GetGatewayIdentityStateResponse) *control.ResourceCondition {
		return state.GetConditions()["identity"].GetConditions()["ClientReady"]
	}
	wait := func(reason string, current bool) *control.GetGatewayIdentityStateResponse {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for {
			state := read()
			value := condition(state)
			if value != nil && value.Reason == reason && value.Current == current {
				return state
			}
			if time.Now().After(deadline) || ctx.Err() != nil {
				t.Fatal("condition did not reach expected state", value)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	stopController := run()
	failed := wait("IdentityProviderUnavailable", true)
	stopController()
	failure := condition(failed)
	if failure.Status != "Unknown" || failure.ObservedGeneration != failed.ResourceGeneration || failure.LastTransitionTime == "" || strings.Contains(failure.Message, "private") || strings.Contains(failure.Message, "credential") || strings.Contains(failure.Message, "do-not-publish") {
		t.Fatal("unsafe failure condition", failure)
	}
	failureTime, err := time.Parse(time.RFC3339Nano, failure.LastTransitionTime)
	if err != nil {
		t.Fatal(err)
	}
	stopAPI()
	stopAPI, _, address = startBoth(t, binary, f.dsn, brokerConfig, settings...)
	public, connection = grpcClient(t, address, apiTLS)
	client = control.NewGatewayIdentityServiceClient(connection)
	restarted := wait("IdentityProviderUnavailable", true)
	if condition(restarted).LastTransitionTime != failure.LastTransitionTime || restarted.ResourceVersion != failed.ResourceVersion {
		t.Fatal("restart changed condition evidence")
	}
	name := "identity-condition-changed"
	if _, err := f.service.Update(ctx, principal("alice"), gateway.ID, gateways.PatchRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	pending := wait("ObservationPending", false)
	if value := condition(pending); value.Status != "Unknown" || value.LastTransitionTime != "" || value.ObservedGeneration >= pending.ResourceGeneration {
		t.Fatal("old evidence appeared current", value)
	}
	oldContext, err := rpc.WithResourceVersion(auth, failed.ResourceVersion)
	if err != nil {
		t.Fatal(err)
	}
	configuration := "{}"
	if _, err := client.ObserveGatewayIdentity(oldContext, &control.ObserveGatewayIdentityRequest{Id: gateway.ID, Oidc: &configuration, Reason: "IdentityClientReady"}); status.Code(err) != codes.Aborted {
		t.Fatal("stale provider result was accepted", err)
	}
	for _, subject := range []string{"alice", "observer"} {
		denied := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, subject)))
		denied, err = rpc.WithResourceVersion(denied, pending.ResourceVersion)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.ObserveGatewayIdentity(denied, &control.ObserveGatewayIdentityRequest{Id: gateway.ID, Oidc: &configuration, Reason: "IdentityClientReady"}); status.Code(err) != codes.PermissionDenied {
			t.Fatal("unauthorized condition accepted", subject, err)
		}
	}
	provider.ready.Store(true)
	stopController = run()
	ready := wait("IdentityClientReady", true)
	stopController()
	ready = read()
	value := condition(ready)
	readyTime, err := time.Parse(time.RFC3339Nano, value.LastTransitionTime)
	if err != nil || value.Status != "True" || value.ObservedGeneration != ready.ResourceGeneration || !readyTime.After(failureTime) || ready.Gateway.GetOidc() != configuration {
		t.Fatal("recovery did not publish current configuration and condition", value, err)
	}
	currentContext, err := rpc.WithResourceVersion(auth, ready.ResourceVersion)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.ObserveGatewayIdentity(currentContext, &control.ObserveGatewayIdentityRequest{Id: gateway.ID, Oidc: &configuration, Reason: "IdentityClientReady"}); err != nil {
		t.Fatal(err)
	}
	stable := read()
	if stable.ResourceVersion != ready.ResourceVersion || condition(stable).LastTransitionTime != value.LastTransitionTime {
		t.Fatal("stable condition caused another write")
	}
	// Failure to enqueue the event must roll back configuration and condition.
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_identity_condition_event CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	changed := `{"changed":true}`
	if _, err := client.ObserveGatewayIdentity(currentContext, &control.ObserveGatewayIdentityRequest{Id: gateway.ID, Oidc: &changed, Reason: "IdentityClientReady"}); err == nil {
		t.Fatal("event failure was ignored")
	}
	unchanged := read()
	if unchanged.ResourceVersion != ready.ResourceVersion || unchanged.Gateway.GetOidc() != configuration || condition(unchanged).LastTransitionTime != value.LastTransitionTime {
		t.Fatal("event failure committed partial identity state")
	}
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_identity_condition_event"); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Delete(ctx, principal("alice"), gateway.ID); err != nil {
		t.Fatal(err)
	}
	deleted := read()
	if !deleted.Deleted || condition(deleted).Current || condition(deleted).Status != "Unknown" {
		t.Fatal("deleted resource retained a current live condition")
	}
	t.Log("Identity failure and transition time survived API restart; recovery, stale writes, grants, and event rollback passed")
}
