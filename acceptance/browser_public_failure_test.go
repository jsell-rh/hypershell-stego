package acceptance

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

func withoutPublicEgress(target []string) []string {
	result := make([]string, 0, len(target))
	for i := 0; i < len(target); i++ {
		if target[i] == "--egress" && i+1 < len(target) && strings.HasPrefix(target[i+1], "gateway-public=") {
			i++
			continue
		}
		result = append(result, target[i])
	}
	return result
}

func TestPublicFailureKeepsRequiredEgress(t *testing.T) {
	target := []string{"--worker", "gateway-workload", "--egress", "kubernetes=192.0.2.1:443", "--egress", "gateway-public=192.0.2.3:443", "--egress", "gateway-postgres=192.0.2.2:5432", "--egress", "gateway-public=[2001:db8::3]:443"}
	original := append([]string{}, target...)
	want := []string{"--worker", "gateway-workload", "--egress", "kubernetes=192.0.2.1:443", "--egress", "gateway-postgres=192.0.2.2:5432"}
	if got := withoutPublicEgress(target); !reflect.DeepEqual(got, want) || !reflect.DeepEqual(target, original) {
		t.Fatal("public fault changed required destinations or source input")
	}
}

func (w *browserGatewayWorkload) checkPublicEgressLoss(id string, restore func()) {
	w.t.Helper()
	beforeSQL := w.checkSQLIsolation()
	token := w.identity.browserLogin(w.t, w.audience(id), "console-alice")
	before, err := w.call("GetProvider", token, `{"name":"browser-provider"}`)
	if err != nil {
		w.t.Fatal("public provider read before recovery failed", status.Code(err))
	}
	started := time.Now()
	deadline := started.Add(90 * time.Second)
	for _, gatewayID := range w.gatewayIDs {
		for {
			response := w.owner.api(w.t, "GET", "/gateways/"+gatewayID, nil)
			var gateway httpapi.Gateway
			if response.StatusCode == 200 && json.Unmarshal(response.Body, &gateway) == nil && gateway.Phase != nil && *gateway.Phase == "Degraded" && gateway.Status != nil && *gateway.Status != "Healthy" && (gateway.RouteAddress == nil || *gateway.RouteAddress == "") {
				break
			}
			if time.Now().After(deadline) {
				w.t.Fatal("public network loss did not clear the healthy endpoint")
			}
			time.Sleep(time.Second)
		}
	}
	restore()
	for _, gatewayID := range w.gatewayIDs {
		w.check(gatewayID)
	}
	if next := w.checkSQLIsolation(); !reflect.DeepEqual(beforeSQL, next) {
		w.t.Fatal("public network recovery changed database or credential identities")
	}
	token = w.identity.browserLogin(w.t, w.audience(id), "console-alice")
	after, err := w.call("GetProvider", token, `{"name":"browser-provider"}`)
	if err != nil || !proto.Equal(before, after) {
		w.t.Fatal("public network recovery changed provider data", status.Code(err))
	}
	record := map[string]any{"gateway_ids": w.gatewayIDs, "worker_public_egress_removed": true, "degraded_addresses_cleared": true, "generated_egress_restored": true, "healthy_addresses_restored": true, "sql_credentials_preserved": true, "provider_data_preserved": true, "seconds": time.Since(started).Seconds()}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		w.t.Fatal(err)
	}
	directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR")
	if directory == "" {
		w.t.Fatal("public failure evidence directory is missing")
	}
	if err = os.WriteFile(filepath.Join(directory, "gateway-public-network-recovery.json"), append(data, '\n'), 0600); err != nil {
		w.t.Fatal(err)
	}
}
