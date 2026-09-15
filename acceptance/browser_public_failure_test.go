package acceptance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
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

func (w *browserGatewayWorkload) checkPublicEgressLoss(id string, remove, restore func()) {
	w.t.Helper()
	readPolicy := func() kube.Object {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		policy, code, err := w.kubernetes.Request(ctx, http.MethodGet, "/apis/networking.k8s.io/v1/namespaces/"+w.p.namespace+"/networkpolicies/"+publicWorkerPolicy, nil)
		if err != nil || code != 200 {
			w.t.Fatal("public worker policy read failed")
		}
		if _, err := publicPolicySpec(policy, w.p.namespace); err != nil {
			w.t.Fatal(err)
		}
		return policy
	}
	for _, gatewayID := range w.gatewayIDs {
		w.check(gatewayID)
	}
	originalPolicy := readPolicy()
	beforeSQL := w.checkSQLIsolation()
	beforeSQLObjects := w.gatewaySQLObjectIDs()
	token := w.identity.browserLogin(w.t, w.audience(id), "console-alice")
	before, err := w.call("GetProvider", token, `{"name":"browser-provider"}`)
	if err != nil {
		w.t.Fatal("public provider read before recovery failed", status.Code(err))
	}
	started := time.Now()
	remove()
	blockedPolicy := readPolicy()
	if err := verifyPublicEgressRemoval(originalPolicy, blockedPolicy, w.p.namespace, w.public.Endpoints); err != nil {
		w.t.Fatal(err)
	}
	deadline := time.Now().Add(90 * time.Second)
	for _, gatewayID := range w.gatewayIDs {
		for {
			response := w.owner.api(w.t, "GET", "/gateways/"+gatewayID, nil)
			var gateway httpapi.Gateway
			if response.StatusCode == 200 && json.Unmarshal(response.Body, &gateway) == nil && gateway.Phase != nil && *gateway.Phase == "Degraded" && gateway.Status != nil && (*gateway.Status == "WorkloadUnavailable" || *gateway.Status == "WorkloadNotReady") && (gateway.RouteAddress == nil || *gateway.RouteAddress == "") {
				break
			}
			if time.Now().After(deadline) {
				w.t.Fatal("public network loss did not clear the healthy endpoint")
			}
			time.Sleep(time.Second)
		}
	}
	restore()
	restoredPolicy := readPolicy()
	if kube.String(restoredPolicy, "metadata", "uid") != kube.String(originalPolicy, "metadata", "uid") || kube.String(restoredPolicy, "metadata", "resourceVersion") == kube.String(blockedPolicy, "metadata", "resourceVersion") || !reflect.DeepEqual(originalPolicy["spec"], restoredPolicy["spec"]) {
		w.t.Fatal("public policy was not restored exactly")
	}
	for _, gatewayID := range w.gatewayIDs {
		w.check(gatewayID)
	}
	if next := w.checkSQLIsolation(); !reflect.DeepEqual(beforeSQL, next) || !reflect.DeepEqual(beforeSQLObjects, w.gatewaySQLObjectIDs()) {
		w.t.Fatal("public network recovery changed database or credential identities")
	}
	token = w.identity.browserLogin(w.t, w.audience(id), "console-alice")
	after, err := w.call("GetProvider", token, `{"name":"browser-provider"}`)
	if err != nil || !proto.Equal(before, after) {
		w.t.Fatal("public network recovery changed provider data", status.Code(err))
	}
	hashPolicy := func(policy kube.Object) string {
		raw, _ := json.Marshal(policy["spec"])
		sum := sha256.Sum256(raw)
		return hex.EncodeToString(sum[:])
	}
	record := map[string]any{"policy_uid": kube.String(originalPolicy, "metadata", "uid"), "before_policy_sha256": hashPolicy(originalPolicy), "blocked_policy_sha256": hashPolicy(blockedPolicy), "restored_policy_sha256": hashPolicy(restoredPolicy), "live_policy_change_verified": true, "baseline_before_fault": true, "sql_object_ids_preserved": true, "gateway_ids": w.gatewayIDs, "worker_public_egress_removed": true, "degraded_addresses_cleared": true, "generated_egress_restored": true, "healthy_addresses_restored": true, "sql_credentials_preserved": true, "provider_data_preserved": true, "seconds": time.Since(started).Seconds()}
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
