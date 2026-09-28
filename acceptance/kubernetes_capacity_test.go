package acceptance

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/sdk"
	"github.com/twmb/franz-go/pkg/kfake"
)

// The capacity run is the workload profile without the browser Deployment.
// It exercises the same worker path as the browser workflow, then measures
// convergence of many real Gateway server Deployments.
func TestGeneratedKubernetesGatewayCapacity(t *testing.T) {
	if os.Getenv("STEGO_TEST_KUBERNETES_CAPACITY") != "1" {
		t.Skip("requires the bounded Kubernetes capacity fixture")
	}
	if os.Getenv("STEGO_TEST_BROWSER_DEPLOYMENT") == "1" {
		t.Fatal("capacity requires the workload profile without the browser Deployment")
	}
	count, err := strconv.Atoi(os.Getenv("STEGO_TEST_CAPACITY_GATEWAYS"))
	if err != nil || count < 1 || count > 200 {
		t.Fatal("STEGO_TEST_CAPACITY_GATEWAYS must be an integer from 1 to 200")
	}
	p := &kubernetesBrowser{t: t, namespace: os.Getenv("STEGO_TEST_NAMESPACE"), group: os.Getenv("STEGO_TEST_FS_GROUP"), oc: os.Getenv("STEGO_TEST_OC"), pods: map[string]string{}, databases: map[string]string{}}
	if !strings.HasPrefix(p.namespace, "stego-service-") || p.group == "" || p.oc == "" {
		t.Fatal("require the dedicated namespace, file group, and cluster client")
	}
	k := startKubernetesKeycloak(t, p.namespace, p.apply, p.command)
	settings, _ := k.apiLoginSetup(t)
	_, telemetry := newAuthenticatedHTTPDiagnosticCollectorAt(t, p.host("fixture"), "0.0.0.0:19093")
	settings = append(settings, telemetry...)
	f := database(t)
	sessions := browserDatabase(t)
	workload, settings := prepareBrowserGatewayWorkload(t, p, f, sessions, k, settings)
	// One hundred Gateways match the production quota exactly. Keep room for
	// the record of this run instead of pinning the boundary.
	settings = append(settings, "HYPERSHELL_SERVICE_ACCOUNT_GATEWAY_QUOTA=200", "HYPERSHELL_SERVICE_ACCOUNT_CREATOR_QUOTA=200")
	aliceID := k.human(t, "capacity-alice")
	response := k.adminRequest(t, "GET", "/clients?clientId=hypershell", nil)
	var clients []struct {
		ID string `json:"id"`
	}
	if json.Unmarshal(response.Body, &clients) != nil || len(clients) != 1 {
		t.Fatal("API client missing")
	}
	response = k.adminRequest(t, "GET", "/clients/"+clients[0].ID+"/roles/gateway:creator", nil)
	var role map[string]any
	if json.Unmarshal(response.Body, &role) != nil {
		t.Fatal("creator role missing")
	}
	k.adminRequest(t, "POST", "/users/"+aliceID+"/role-mappings/clients/"+clients[0].ID, []any{role})
	host := p.host("fixture")
	_, brokerConfig := broker(t, identity(t, host), kfake.ListenFn(func(network, address string) (net.Listener, error) {
		ln, err := net.Listen("tcp", "0.0.0.0:19092")
		if err != nil {
			return nil, err
		}
		return advertisedListener{ln, serviceAddress(host + ":19092")}, nil
	}))
	apiIdentity := identity(t, p.host("hypershell"))
	dir := filepath.Dir(apiIdentity.config.CAFile)
	settings = append(settings, "STEGO_HTTP_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_HTTP_TLS_KEY="+filepath.Join(dir, "server-key.pem"), "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"))
	settings = append(settings, "HYPERSHELL_DEFAULT_GATEWAY_RELEASE_ID="+f.release, "HYPERSHELL_DEFAULT_GATEWAY_CLUSTER_ID="+f.cluster)
	stopAPI, _, rpc := p.startAPI(f, brokerConfig, apiIdentity, settings)
	defer func() { stopAPI() }()
	aliceToken := k.browserLogin(t, "hypershell", "capacity-alice")
	owner, err := sdk.NewClient(sdk.Options{BaseURL: "https://" + p.host("hypershell") + ":8443", CAFile: apiIdentity.config.CAFile, Token: aliceToken})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(owner.Close)
	type capacityGateway struct {
		id, namespace string
		healthy       time.Time
		deployed      time.Time
		total         float64
	}
	gateways := make([]*capacityGateway, count)
	created := time.Now()
	for i := range gateways {
		input := sdk.CreateGatewayJSONRequestBody{Name: fmt.Sprintf("capacity-gateway-%03d", i), ClusterId: f.cluster, ReleaseId: f.release}
		result, err := owner.CreateGatewayWithResponse(context.Background(), input)
		if err != nil || result.JSON201 == nil || result.JSON201.Id == nil || result.JSON201.Namespace == nil {
			t.Fatal("capacity Gateway creation failed", i, err)
		}
		gateways[i] = &capacityGateway{id: *result.JSON201.Id, namespace: *result.JSON201.Namespace}
	}
	creationSeconds := time.Since(created).Seconds()
	workload.startWorkers(rpc, apiIdentity.config.CAFile)
	roots := x509.NewCertPool()
	ca, err := os.ReadFile(apiIdentity.config.CAFile)
	if err != nil || !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid API certificate authority")
	}
	transport := &http.Transport{MaxIdleConnsPerHost: 16, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots}, ResponseHeaderTimeout: 10 * time.Second}
	defer transport.CloseIdleConnections()
	status := func(value *string) string {
		if value == nil {
			return "absent"
		}
		return *value
	}
	check := func(ctx context.Context, gateway *capacityGateway) error {
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://"+p.host("hypershell")+":8443/api/hypershell/v1/gateways/"+gateway.id, nil)
		if err != nil {
			return err
		}
		request.Header.Set("Authorization", "Bearer "+aliceToken)
		answer, err := transport.RoundTrip(request)
		if err != nil {
			return err
		}
		body, err := io.ReadAll(io.LimitReader(answer.Body, 1<<20))
		answer.Body.Close()
		if err != nil {
			return err
		}
		if answer.StatusCode != http.StatusOK {
			return fmt.Errorf("gateway status %d", answer.StatusCode)
		}
		var observed gatewayResponse
		if json.Unmarshal(body, &observed) != nil {
			return fmt.Errorf("invalid gateway response")
		}
		if observed.Status == nil || *observed.Status != "Healthy" || observed.Phase == nil || *observed.Phase != "Running" {
			return fmt.Errorf("gateway status %s phase %s", status(observed.Status), status(observed.Phase))
		}
		if gateway.healthy.IsZero() {
			gateway.healthy = time.Now()
		}
		object, code, err := workload.kubernetes.Request(ctx, http.MethodGet, "/apis/apps/v1/namespaces/"+gateway.namespace+"/deployments/openshell-gateway", nil)
		if err != nil {
			return err
		}
		var state struct {
			Metadata struct{ Generation int64 }
			Status   struct {
				ObservedGeneration             int64
				ReadyReplicas, UpdatedReplicas int
			}
		}
		data, _ := json.Marshal(object)
		if code != http.StatusOK || json.Unmarshal(data, &state) != nil || state.Metadata.Generation == 0 || state.Status.ObservedGeneration < state.Metadata.Generation || state.Status.ReadyReplicas != 1 || state.Status.UpdatedReplicas != 1 {
			return fmt.Errorf("deployment not ready (code %d)", code)
		}
		return nil
	}
	deadline := time.Now().Add(25 * time.Minute)
	for remaining := gateways; len(remaining) > 0; {
		if time.Now().After(deadline) {
			for _, logs := range workload.outputs {
				t.Log(logs())
			}
			var pending []string
			for _, gateway := range gateways {
				if gateway.deployed.IsZero() {
					pending = append(pending, gateway.id)
				}
			}
			t.Fatal("capacity Gateways did not converge", len(pending), strings.Join(pending, ","))
		}
		before := len(remaining)
		var wait sync.WaitGroup
		for _, gateway := range remaining {
			wait.Add(1)
			go func(gateway *capacityGateway) {
				defer wait.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
				defer cancel()
				if err := check(ctx, gateway); err == nil {
					gateway.deployed = time.Now()
				}
			}(gateway)
		}
		wait.Wait()
		next := remaining[:0]
		for _, gateway := range remaining {
			if gateway.deployed.IsZero() {
				next = append(next, gateway)
			}
		}
		remaining = next
		if len(remaining) == before {
			time.Sleep(5 * time.Second)
		}
	}
	for _, gateway := range gateways {
		gateway.total = gateway.deployed.Sub(created).Seconds()
	}
	sort.Slice(gateways, func(i, j int) bool { return gateways[i].total < gateways[j].total })
	evidence := struct {
		Converged              int     `json:"converged"`
		Requested              int     `json:"requested"`
		CreationWallSeconds    float64 `json:"creation_wall_seconds"`
		SlowestTotalSeconds    float64 `json:"slowest_total_seconds"`
		MedianTotalSeconds     float64 `json:"median_total_seconds"`
		WorkerReplicas         int     `json:"worker_replicas"`
		GatewayCPURequestMilli int     `json:"gateway_cpu_request_milli"`
	}{
		Converged:              len(gateways),
		Requested:              count,
		CreationWallSeconds:    creationSeconds,
		SlowestTotalSeconds:    gateways[len(gateways)-1].total,
		MedianTotalSeconds:     gateways[len(gateways)/2].total,
		WorkerReplicas:         3,
		GatewayCPURequestMilli: 50,
	}
	directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR")
	if directory == "" {
		t.Fatal("capacity evidence requires STEGO_BROWSER_ARTIFACT_DIR")
	}
	data, err := json.MarshalIndent(evidence, "", "  ")
	if err != nil || os.WriteFile(filepath.Join(directory, "gateway-capacity.json"), append(data, '\n'), 0600) != nil {
		t.Fatal("cannot write capacity evidence")
	}
	if len(p.pods) != 4 {
		t.Fatal("all generated Deployments must run")
	}
	t.Logf("Generated capacity run converged %d Gateways in %.1f seconds", len(gateways), evidence.SlowestTotalSeconds)
}
