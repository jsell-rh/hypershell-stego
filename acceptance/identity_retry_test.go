package acceptance

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type timedIdentityFailure struct {
	*failedIdentityProvider
	attempts chan time.Time
}

func (p *timedIdentityFailure) EnsureGateway(ctx context.Context, id, name string) (string, error) {
	select {
	case p.attempts <- time.Now():
	case <-ctx.Done():
		return "", ctx.Err()
	}
	return p.failedIdentityProvider.EnsureGateway(ctx, id, name)
}

func TestIdentityRetrySurvivesAPIWatchRestart(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("retry-restart"))
	if err != nil {
		t.Fatal(err)
	}
	_, brokerConfig := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	settings = withControllerWriteGrants(t, settings, writeGrant("controller", "configure.identity", ""))
	binary := buildApplication(t)
	stopAPI, httpAddress, address := startBoth(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stopAPI() }()
	public, connection := grpcClient(t, address, apiTLS)
	client := control.NewGatewayIdentityServiceClient(connection)
	auth := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "controller")))
	provider := &timedIdentityFailure{failedIdentityProvider: &failedIdentityProvider{checkpointProvider: &checkpointProvider{roles: make(map[string][]string)}}, attempts: make(chan time.Time, 16)}
	controller, err := gatewayidentity.New(public, client, provider)
	if err != nil {
		t.Fatal(err)
	}
	work, stopController := context.WithCancel(auth)
	done := make(chan error, 1)
	go func() { done <- controller.Run(work) }()
	defer func() {
		stopController()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("controller did not stop")
		}
	}()
	var fourth time.Time
	for i := 0; i < 4; i++ {
		select {
		case fourth = <-provider.attempts:
		case <-ctx.Done():
			t.Fatal("provider retries did not run")
		}
	}
	read := func() *control.GetGatewayIdentityStateResponse {
		value, err := observationRead(auth, func(call context.Context) (*control.GetGatewayIdentityStateResponse, error) {
			return client.GetGatewayIdentityState(call, &control.GetGatewayIdentityStateRequest{Id: gateway.ID}, grpc.WaitForReady(true))
		})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	before := read()
	condition := before.GetConditions()["identity"].GetConditions()["ClientReady"]
	if !condition.GetCurrent() || condition.GetStatus() != "Unknown" || condition.GetReason() != "IdentityProviderUnavailable" {
		t.Fatal("failed provider has no current failure condition", condition)
	}
	stopAPI()
	// Bind the same gRPC address so the live controller must recover its stream.
	restartSettings := append(append([]string{}, settings...), "STEGO_GRPC_ADDR="+address)
	var restartedAddress string
	stopAPI, httpAddress, restartedAddress = startBoth(t, binary, f.dsn, brokerConfig, restartSettings...)
	if restartedAddress != address {
		t.Fatal("API did not reuse its gRPC address", address, restartedAddress)
	}
	if time.Since(fourth) >= 6*time.Second {
		t.Fatal("API restart consumed the retry observation window")
	}
	code, _ := requestJSON(t, "GET", httpAddress+"/api/hypershell/v1/gateways/"+gateway.ID, token(t, key, "alice"), nil)
	if code != 200 {
		t.Fatal("REST Gateway read failed after restart", code)
	}
	after := read()
	if after.ResourceVersion != before.ResourceVersion || after.GetConditions()["identity"].GetConditions()["ClientReady"].GetLastTransitionTime() != condition.GetLastTransitionTime() {
		t.Fatal("API restart changed durable failure evidence")
	}
	var fifth time.Time
	select {
	case fifth = <-provider.attempts:
	case <-ctx.Done():
		t.Fatal("provider retry did not resume")
	}
	if delay := fifth.Sub(fourth); delay < 8*time.Second {
		t.Fatal("API watch restart bypassed the provider retry delay", delay)
	}
	t.Log("REST and gRPC recovered after API restart; the live identity controller retained its retry delay and failure condition")
}
