package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGatewayControllerWriteGrantsAcrossPlacementAndRestart(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	row, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("write-scopes"))
	if err != nil {
		t.Fatal(err)
	}
	// Record committed Gateway events even after the runtime deletes outbox rows.
	if _, err := f.db.Exec(`CREATE TABLE gateway_event_audit(kind text NOT NULL);
CREATE FUNCTION audit_gateway_event() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN INSERT INTO gateway_event_audit VALUES (NEW.kind); RETURN NEW; END $$;
CREATE TRIGGER audit_gateway_event AFTER INSERT ON stego_outbox.messages
FOR EACH ROW WHEN (NEW.kind LIKE 'gateway.%') EXECUTE FUNCTION audit_gateway_event()`); err != nil {
		t.Fatal(err)
	}
	second := ksuid.New().String()
	if err := f.storage.Create(ctx, "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: second}, Name: "second", Provider: "kubernetes", KubeconfigSecret: "unused"}); err != nil {
		t.Fatal(err)
	}
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["workload","identity","second","ungranted","console","cleanup","endpoint-only","workload-only"]`)
	settings = withCleanupGrants(t, settings, cleanupGrant("cleanup", "Gateway", "workload", f.cluster))
	settings = withControllerWriteGrants(t, settings, writeGrant("workload", "observe.workload", f.cluster), writeGrant("identity", "configure.identity", ""), writeGrant("second", "observe.workload", second), writeGrant("console", "configure.console", f.cluster), writeGrant("ordinary", "observe.workload", f.cluster), writeGrant("workload", "observe.endpoint", f.cluster), writeGrant("second", "observe.endpoint", second), writeGrant("endpoint-only", "observe.endpoint", f.cluster), writeGrant("workload-only", "observe.workload", f.cluster))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	client, connection := grpcClient(t, grpcAddress, tlsIdentity)
	readGatewayEvent(t, consumer, row.ID, "Create", "gateway.created")
	awaitQueueEmpty(t, f)
	state := func() model.Gateway {
		t.Helper()
		value, err := f.storage.Get(ctx, "Gateway", row.ID)
		if err != nil {
			t.Fatal(err)
		}
		return value.(model.Gateway)
	}
	workloadToken := token(t, key, "workload", "platform:admin")
	write := func(bearer string, request *pb.UpdateGatewayRequest, want codes.Code) {
		t.Helper()
		before := state()
		beforeEvents := count(t, f.db, "gateway_event_audit")
		call, err := rpc.WithResourceVersion(metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer)), before.ResourceVersion)
		if err != nil {
			t.Fatal(err)
		}
		request.Id = row.ID
		_, err = client.UpdateGateway(call, request)
		if status.Code(err) != want {
			t.Fatalf("controller write: %v, want %v", err, want)
		}
		if want != codes.OK {
			after := state()
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("denied write changed state: revision %d to %d", before.ResourceVersion, after.ResourceVersion)
			}
			if n := count(t, f.db, "gateway_event_audit"); n != beforeEvents {
				t.Fatalf("denied write changed events: %d to %d", beforeEvents, n)
			}
		} else {
			if count(t, f.db, "gateway_event_audit") != beforeEvents+1 {
				t.Fatal("write did not commit one Gateway event")
			}
			if state().ResourceVersion <= before.ResourceVersion {
				t.Fatal("write did not advance revision")
			}
			readGatewayEvent(t, consumer, row.ID, "Update", "gateway.updated")
			awaitQueueEmpty(t, f)
		}
	}
	statusPatch := func() *pb.UpdateGatewayRequest {
		return &pb.UpdateGatewayRequest{Phase: pointer("Running"), Status: pointer("Healthy")}
	}
	for _, subject := range []string{"identity", "second", "ungranted", "ordinary", "cleanup", "console"} {
		write(token(t, key, subject, "platform:admin"), statusPatch(), codes.PermissionDenied)
	}
	write(workloadToken, &pb.UpdateGatewayRequest{Oidc: pointer(`{"issuer":"wrong"}`)}, codes.PermissionDenied)
	write(workloadToken, &pb.UpdateGatewayRequest{ConsoleAddress: pointer("https://wrong.example")}, codes.PermissionDenied)
	write(workloadToken, &pb.UpdateGatewayRequest{Name: pointer("wrong")}, codes.PermissionDenied)
	write(workloadToken, &pb.UpdateGatewayRequest{ClusterId: &second, Phase: pointer("Running"), Status: pointer("Healthy")}, codes.InvalidArgument)
	write(token(t, key, "identity"), &pb.UpdateGatewayRequest{Oidc: pointer("{}"), Name: pointer("wrong")}, codes.InvalidArgument)
	write(workloadToken, statusPatch(), codes.OK)
	write(token(t, key, "identity"), &pb.UpdateGatewayRequest{Oidc: pointer("{}")}, codes.OK)
	write(token(t, key, "console"), &pb.UpdateGatewayRequest{ConsoleAddress: pointer("https://console.example")}, codes.OK)
	endpointPatch := func(value string) *pb.UpdateGatewayRequest {
		return &pb.UpdateGatewayRequest{RouteAddress: &value}
	}
	endpoint := "https://gateway.example.test"
	for _, subject := range []string{"identity", "second", "ungranted", "ordinary", "cleanup", "console", "alice"} {
		write(token(t, key, subject, "platform:admin"), endpointPatch(endpoint), codes.PermissionDenied)
	}
	combinedPatch := func(address, phase, state string) *pb.UpdateGatewayRequest {
		return &pb.UpdateGatewayRequest{RouteAddress: &address, Phase: &phase, Status: &state}
	}
	for _, subject := range []string{"endpoint-only", "workload-only", "second", "identity", "alice"} {
		write(token(t, key, subject, "platform:admin"), combinedPatch(endpoint, "Running", "Healthy"), codes.PermissionDenied)
	}
	write(workloadToken, &pb.UpdateGatewayRequest{RouteAddress: &endpoint, ConsoleAddress: pointer(endpoint)}, codes.InvalidArgument)
	write(workloadToken, &pb.UpdateGatewayRequest{RouteAddress: &endpoint, ClusterId: &second}, codes.InvalidArgument)
	write(workloadToken, endpointPatch("invalid\x00"), codes.InvalidArgument)
	beforeEndpoint := state()
	// Reject the second observation after the first SQL update. Both groups
	// and the event must roll back in the generated transaction.
	if _, err := f.db.Exec(`ALTER TABLE gateways ADD CONSTRAINT reject_test_endpoint CHECK (route_address IS NULL OR route_address <> 'https://rejected.example.test')`); err != nil {
		t.Fatal(err)
	}
	write(workloadToken, combinedPatch("https://rejected.example.test", "Degraded", "RouteNotReady"), codes.Internal)
	if _, err := f.db.Exec(`ALTER TABLE gateways DROP CONSTRAINT reject_test_endpoint`); err != nil {
		t.Fatal(err)
	}
	write(workloadToken, combinedPatch(endpoint, "Running", "Healthy"), codes.OK)
	published := state()
	if published.ResourceGeneration != beforeEndpoint.ResourceGeneration || published.ObservedGeneration("endpoint") != published.ResourceGeneration || published.ObservedGeneration("workload") != published.ResourceGeneration || published.CurrentObservations().Phase == nil || *published.CurrentObservations().Phase != "Running" || published.CurrentObservations().Status == nil || *published.CurrentObservations().Status != "Healthy" || published.CurrentObservations().RouteAddress == nil || *published.CurrentObservations().RouteAddress != endpoint {
		t.Fatal("endpoint observation changed desired generation or was not published")
	}
	stale, err := rpc.WithResourceVersion(metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+workloadToken)), beforeEndpoint.ResourceVersion)
	if err != nil {
		t.Fatal(err)
	}
	staleEvents := count(t, f.db, "gateway_event_audit")
	if _, err := client.UpdateGateway(stale, &pb.UpdateGatewayRequest{Id: row.ID, RouteAddress: pointer(""), Phase: pointer("Degraded"), Status: pointer("RouteNotReady")}); status.Code(err) != codes.Aborted {
		t.Fatal("stale endpoint observation was accepted", err)
	}
	if !reflect.DeepEqual(published, state()) || count(t, f.db, "gateway_event_audit") != staleEvents {
		t.Fatal("stale endpoint observation changed state or events")
	}
	root := address + "/api/hypershell/v1/gateways/" + row.ID
	owner := token(t, key, "alice")
	for _, value := range []string{`null`, `""`, `"https://untrusted.example"`} {
		if code, _ := requestJSON(t, "PATCH", root, owner, []byte(`{"name":"must-not-change","route_address":`+value+`}`)); code != 400 {
			t.Fatal("owner REST patch accepted route_address", code)
		}
		body := []byte(fmt.Sprintf(`{"name":"must-not-create","cluster_id":%q,"release_id":%q,"route_address":%s}`, f.cluster, f.release, value))
		if code, _ := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", token(t, key, "alice", "gateway:creator"), body); code != 400 {
			t.Fatal("REST creation accepted route_address", code)
		}
	}
	for _, value := range []string{"", endpoint} {
		if _, err := client.UpdateGateway(metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+owner)), &pb.UpdateGatewayRequest{Id: row.ID, RouteAddress: &value}); status.Code(err) != codes.PermissionDenied {
			t.Fatal("owner gRPC update accepted route_address", err)
		}
	}
	if !reflect.DeepEqual(published, state()) || count(t, f.db, "gateway_event_audit") != staleEvents || count(t, f.db, "gateways") != 1 {
		t.Fatal("rejected owner endpoint write changed state or events")
	}
	for _, bearer := range []string{owner, workloadToken} {
		code, body := requestJSON(t, "GET", root, bearer, nil)
		var response struct {
			RouteAddress string `json:"route_address"`
		}
		if code != 200 || json.Unmarshal(body, &response) != nil || response.RouteAddress != endpoint {
			t.Fatal("REST read lost the controller endpoint", code)
		}
	}
	write(workloadToken, combinedPatch("", "Degraded", "RouteNotReady"), codes.OK)
	if value := state().CurrentObservations().RouteAddress; value == nil || *value != "" {
		t.Fatal("controller could not clear the endpoint")
	}
	if current := state().CurrentObservations(); current.Status == nil || *current.Status != "RouteNotReady" || current.Phase == nil || *current.Phase != "Degraded" {
		t.Fatal("endpoint failure did not clear workload readiness")
	}
	write(workloadToken, combinedPatch(endpoint, "Running", "Healthy"), codes.OK)
	before := state()
	if code, _ := requestJSON(t, "PATCH", root, workloadToken, []byte(`{"oidc":"wrong"}`)); code != 428 {
		t.Fatal("REST controller bypass", code)
	}
	if !reflect.DeepEqual(before, state()) {
		t.Fatal("REST denial changed state")
	}
	patch, _ := json.Marshal(map[string]string{"cluster_id": second})
	events := count(t, f.db, "gateway_event_audit")
	if code, _ := requestJSON(t, "PATCH", root, token(t, key, "alice"), patch); code != 409 {
		t.Fatal("Gateway move changed its assigned cluster", code)
	}
	if !reflect.DeepEqual(before, state()) || count(t, f.db, "gateway_event_audit") != events {
		t.Fatal("denied move changed Gateway state or events")
	}
	connection.Close()
	stop()
	restoreGatewayClusterHistory(t, f, row.ID, second)
	stop, _, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	client, connection = grpcClient(t, grpcAddress, tlsIdentity)
	write(workloadToken, statusPatch(), codes.PermissionDenied)
	write(workloadToken, endpointPatch(endpoint), codes.PermissionDenied)
	if value := state().CurrentObservations().RouteAddress; value == nil || *value != "" {
		t.Fatal("placement change retained an old public endpoint")
	}
	write(token(t, key, "console"), &pb.UpdateGatewayRequest{ConsoleAddress: pointer("https://wrong.example")}, codes.PermissionDenied)
	secondToken := token(t, key, "second")
	write(secondToken, combinedPatch(endpoint, "Running", "Healthy"), codes.OK)
	connection.Close()
	stop()
	settings = withControllerWriteGrants(t, settings, writeGrant("identity", "configure.identity", ""))
	stop, _, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	client, _ = grpcClient(t, grpcAddress, tlsIdentity)
	write(secondToken, statusPatch(), codes.PermissionDenied)
	write(secondToken, endpointPatch(""), codes.PermissionDenied)
	write(secondToken, combinedPatch("", "Degraded", "RouteNotReady"), codes.PermissionDenied)
	write(token(t, key, "identity"), &pb.UpdateGatewayRequest{Oidc: pointer(`{"issuer":"updated"}`)}, codes.OK)
}
