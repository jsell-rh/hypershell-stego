package acceptance

import (
	"context"
	"encoding/json"
	"github.com/jsell-rh/hypershell-stego/contracts"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"path/filepath"
	"testing"
	"time"
)

type phaseCountsResponse struct {
	Kind   string         `json:"kind"`
	Href   string         `json:"href"`
	Counts map[string]int `json:"counts"`
}

// The metrics route counts visible gateways per workload phase. Counts follow
// the list visibility rule. Controller-owned phases are read-only for users.
func TestGatewayPhaseCountsThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`, "HYPERSHELL_DEFAULT_GATEWAY_CREATOR=false")
	settings = withControllerWriteGrants(t, settings, writeGrant("controller", "observe.workload", f.cluster))
	binary := buildApplication(t)
	stop, httpAddress, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	client, connection := grpcClient(t, grpcAddress, tlsIdentity)
	defer func() { _ = connection.Close() }()
	state := control.NewGatewayIdentityServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	creator := token(t, key, "alice", "gateway:creator")
	owner := token(t, key, "alice")
	admin := token(t, key, "admin", "platform:admin")
	controller := call(token(t, key, "controller"))
	path := httpAddress + "/api/hypershell/v1/metrics/gateways"
	create := func(name string) string {
		t.Helper()
		code, data := requestJSON(t, "POST", httpAddress+"/api/hypershell/v1/gateways", creator, []byte(`{"name":"`+name+`","cluster_id":"`+f.cluster+`","release_id":"`+f.release+`"}`))
		var gateway gatewayResponse
		if code != 201 || json.Unmarshal(data, &gateway) != nil {
			t.Fatalf("create %s: %d %s", name, code, data)
		}
		return gateway.ID
	}
	setPhase := func(id, phase, status string) {
		t.Helper()
		observed, err := state.GetGatewayIdentityState(controller, &control.GetGatewayIdentityStateRequest{Id: id})
		if err != nil {
			t.Fatalf("observe %s: %v", id, err)
		}
		versioned, err := rpc.WithResourceVersion(controller, observed.ResourceVersion)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.UpdateGateway(versioned, &pb.UpdateGatewayRequest{Id: id, Phase: pointer(phase), Status: pointer(status)}); err != nil {
			t.Fatalf("set phase %s on %s: %v", phase, id, err)
		}
	}
	for _, query := range []string{"?fields=counts", "?page=1"} {
		if code, _ := requestJSON(t, "GET", path+query, admin, nil); code != 400 {
			t.Fatal("metrics query accepted", query)
		}
	}
	if code, _ := requestJSON(t, "GET", path, admin, []byte(`{}`)); code != 400 {
		t.Fatal("metrics body accepted")
	}
	var empty phaseCountsResponse
	if code, data := requestJSON(t, "GET", path, admin, nil); code != 200 || json.Unmarshal(data, &empty) != nil || empty.Kind != "GatewayPhaseCounts" || empty.Href != "/api/hypershell/v1/metrics/gateways" || len(empty.Counts) != 4 {
		t.Fatalf("empty metrics: %d %s", code, data)
	}
	for _, phase := range []string{"Running", "Provisioning", "Degraded", "Failed"} {
		if empty.Counts[phase] != 0 {
			t.Fatalf("empty metrics count %s: %d", phase, empty.Counts[phase])
		}
	}
	document, err := contracts.LoadActiveOpenAPI(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	schema := document.Paths.Value("/api/hypershell/v1/metrics/gateways").Get.Responses.Status(200).Value.Content.Get("application/json").Schema.Value

	running := create("metrics-running")
	degraded := create("metrics-degraded")
	pending := create("metrics-pending")
	setPhase(running, "Running", "Healthy")
	setPhase(degraded, "Degraded", "WorkloadUnavailable")
	setPhase(pending, "Provisioning", "WorkloadNotReady")

	read := func(bearer string) phaseCountsResponse {
		t.Helper()
		code, data := requestJSON(t, "GET", path, bearer, nil)
		var result phaseCountsResponse
		if code != 200 || json.Unmarshal(data, &result) != nil {
			t.Fatalf("metrics read: %d %s", code, data)
		}
		var value any
		if err := json.Unmarshal(data, &value); err != nil {
			t.Fatal(err)
		}
		if err := schema.VisitJSON(value); err != nil {
			t.Fatalf("metrics response violates the active contract: %v", err)
		}
		return result
	}
	fleet := read(admin)
	if fleet.Counts["Running"] != 1 || fleet.Counts["Degraded"] != 1 || fleet.Counts["Provisioning"] != 1 || fleet.Counts["Failed"] != 0 {
		t.Fatalf("fleet counts: %v", fleet.Counts)
	}
	// The creator owns every gateway, so the visibility filter keeps the counts.
	visible := read(owner)
	if visible.Counts["Running"] != 1 || visible.Counts["Degraded"] != 1 || visible.Counts["Provisioning"] != 1 {
		t.Fatalf("visible counts: %v", visible.Counts)
	}
	if _, err := client.UpdateGateway(call(admin), &pb.UpdateGatewayRequest{Id: running, Phase: pointer("Running"), Status: pointer("Healthy")}); status.Code(err) != codes.PermissionDenied {
		t.Fatalf("user phase write: %v", err)
	}
	stranger := read(token(t, key, "stranger"))
	for _, phase := range []string{"Running", "Provisioning", "Degraded", "Failed"} {
		if stranger.Counts[phase] != 0 {
			t.Fatalf("stranger counts %s: %d", phase, stranger.Counts[phase])
		}
	}
}
