package gatewayworkload

import (
	"context"
	"errors"
	"testing"

	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestPublicEndpointObservation(t *testing.T) {
	endpoint, empty := "https://gateway.example.test", ""
	for _, scenario := range []string{"healthy", "failed", "pending", "conflict", "unchanged", "disabled", "internal"} {
		t.Run(scenario, func(t *testing.T) {
			gw, release := records(t)
			phase, state := "Running", "Healthy"
			gw.Phase, gw.Status = &phase, &state
			gw.RouteAddress = &endpoint
			provider := &providerFixture{endpoint: &endpoint}
			api := new(apiFixture)
			switch scenario {
			case "healthy", "conflict":
				gw.RouteAddress = &empty
			case "failed":
				provider.err = errors.New("private provider failure")
			case "pending":
				provider.err = ErrPending
			case "disabled":
				provider.endpoint = &empty
			case "internal":
				provider.endpoint = nil
				gw.RouteAddress = &empty
				state = "ObservationPending"
			}
			if scenario == "conflict" {
				api.err = status.Error(codes.Aborted, "resource changed")
			}
			current := &stateFixture{state: &control.GetGatewayIdentityStateResponse{CleanupTargets: workloadHistory(testClusterID), Gateway: gw, ResourceVersion: 77, ResourceGeneration: 3, ObservedGeneration: 3}}
			c, err := New(api, current, &releaseFixture{row: release}, provider)
			if err != nil {
				t.Fatal(err)
			}
			_, err = c.reconcile(context.Background(), gw.Metadata.Id)
			if provider.creates != 1 {
				t.Fatal("endpoint was not checked before observation")
			}
			if scenario == "conflict" && status.Code(err) != codes.Aborted {
				t.Fatal("stale observation was not rejected", err)
			}
			if scenario == "unchanged" {
				if err != nil || api.updates != 0 {
					t.Fatal("unchanged verified endpoint produced a write", err)
				}
				return
			}
			if api.updates != 1 || api.version != "77" {
				t.Fatal("observation did not retain the original revision", api.updates, api.version)
			}
			if scenario == "internal" {
				if api.endpoint != nil {
					t.Fatal("internal workload requested endpoint authority")
				}
				return
			}
			if api.endpoint == nil {
				t.Fatal("public observation omitted its endpoint")
			}
			switch scenario {
			case "failed", "pending":
				if *api.endpoint != "" || api.phase != "Degraded" || api.desired == "Healthy" {
					t.Fatal("failed public check retained a healthy endpoint")
				}
			case "disabled":
				if err != nil || *api.endpoint != "" {
					t.Fatal("disabled endpoint was not cleared", err)
				}
			default:
				if *api.endpoint != endpoint || api.phase != "Running" || api.desired != "Healthy" {
					t.Fatal("verified public endpoint was not published with health")
				}
			}
		})
	}
}
