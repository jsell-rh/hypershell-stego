package gatewayworkload

import (
	"context"
	"errors"
	"testing"
	"time"

	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
)

func TestConsoleAddressRequiresSuccessfulCurrentObservation(t *testing.T) {
	for _, test := range []struct {
		name      string
		err       error
		published bool
	}{
		{"ready", nil, true},
		{"pending", ErrPending, false},
		{"failure", errors.New("verification failed"), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			gw, release := records(t)
			address := "https://console.example.test"
			gw.ConsoleAddress = &address
			api := &apiFixture{}
			state := &stateFixture{state: &control.GetGatewayIdentityStateResponse{Gateway: gw, ResourceVersion: 42, ResourceGeneration: 3, CleanupTargets: workloadHistory(testClusterID)}}
			provider := &providerFixture{console: &address, err: test.err}
			controller, err := New(api, state, &releaseFixture{row: release}, provider)
			if err != nil {
				t.Fatal(err)
			}
			result, err := controller.reconcile(context.Background(), gw.Metadata.Id)
			wantError := test.err
			if test.err == ErrPending {
				wantError = nil
				if result.RecheckAfter != time.Second {
					t.Fatal("pending console has no scheduled check")
				}
			}
			if !errors.Is(err, wantError) || provider.ensuredVersion != 42 || api.version != "42" || api.updates != 1 || api.console == nil {
				t.Fatal("console observation lost its verification or resource version", err)
			}
			want := ""
			if test.published {
				want = address
			}
			if *api.console != want {
				t.Fatal("console address differs from the verified observation")
			}
		})
	}
}
