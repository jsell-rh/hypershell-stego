package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// A controller must not publish success from an observation made before a
// desired-state change. A fresh transaction alone cannot detect this conflict.
func TestGatewayRejectsOldObservationAcrossRESTGRPCAndRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	binary := buildApplication(t)
	stop, httpAddress, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	client, connection := grpcClient(t, grpcAddress, tlsIdentity)
	stateClient := control.NewGatewayIdentityServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	owner := token(t, key, "alice")
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	controller := call(token(t, key, "controller"))
	path := httpAddress + "/api/hypershell/v1/gateways"
	code, data := requestJSON(t, "POST", path, token(t, key, "alice", "gateway:creator"), []byte(fmt.Sprintf(`{"name":"version-check","cluster_id":%q,"release_id":%q,"database_id":"ignored"}`, f.cluster, f.release)))
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(data, &gateway) != nil {
		t.Fatalf("create: %d %s", code, data)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Create", "gateway.created")
	awaitQueueEmpty(t, f)
	observe := func() *control.GetGatewayIdentityStateResponse {
		t.Helper()
		row, err := stateClient.GetGatewayIdentityState(controller, &control.GetGatewayIdentityStateRequest{Id: gateway.ID})
		if err != nil || row.GetResourceVersion() < 1 {
			t.Fatalf("observe: %v %v", row, err)
		}
		return row
	}
	observed := observe()
	versioned := func(parent context.Context, revision int64) context.Context {
		t.Helper()
		result, err := rpc.WithResourceVersion(parent, revision)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	update := &pb.UpdateGatewayRequest{Id: gateway.ID, Phase: pointer("Running"), Status: pointer("Healthy")}
	code, data = requestJSON(t, "PATCH", path+"/"+gateway.ID, owner, []byte(`{"external_dns":"changed.example.test"}`))
	if code != 200 {
		t.Fatalf("desired change: %d %s", code, data)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	awaitQueueEmpty(t, f)
	current := observe()
	if current.ResourceVersion <= observed.ResourceVersion {
		t.Fatal("REST write did not advance revision")
	}
	reject := func(callContext context.Context, want codes.Code) {
		t.Helper()
		if _, err := client.UpdateGateway(callContext, update); status.Code(err) != want {
			t.Fatalf("status write: %v, want %v", err, want)
		}
	}
	reject(versioned(controller, observed.ResourceVersion), codes.Aborted)
	reject(controller, codes.FailedPrecondition)
	for _, values := range [][]string{{"0"}, {"01"}, {"-1"}, {"1", "1"}, {"9223372036854775808"}} {
		md, _ := metadata.FromOutgoingContext(controller)
		md = md.Copy()
		md.Set("if-resource-version", values...)
		reject(metadata.NewOutgoingContext(ctx, md), codes.InvalidArgument)
	}
	reject(versioned(call(owner), current.ResourceVersion), codes.PermissionDenied)
	code, data = requestJSON(t, "PATCH", path+"/"+gateway.ID, token(t, key, "controller"), []byte(`{"status":"Healthy"}`))
	if code != 428 {
		t.Fatalf("REST bypass: %d %s", code, data)
	}
	unchanged := observe()
	if unchanged.ResourceVersion != current.ResourceVersion || unchanged.Gateway.GetStatus() == "Healthy" || count(t, f.db, "stego_outbox.messages") != 0 {
		t.Fatal("rejected observation changed state or queued an event")
	}
	// A failed event insert must also roll back the revision and status.
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_version_event CHECK(false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	reject(versioned(controller, current.ResourceVersion), codes.Internal)
	if row := observe(); row.ResourceVersion != current.ResourceVersion || row.Gateway.GetStatus() == "Healthy" {
		t.Fatal("event failure committed the conditional write")
	}
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_version_event`); err != nil {
		t.Fatal(err)
	}
	// Restart must preserve both the current revision and the old-token rejection.
	stop()
	connection.Close()
	_, httpAddress, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	client, connection = grpcClient(t, grpcAddress, tlsIdentity)
	stateClient = control.NewGatewayIdentityServiceClient(connection)
	if row := observe(); row.ResourceVersion != current.ResourceVersion {
		t.Fatal("restart changed revision")
	}
	reject(versioned(controller, observed.ResourceVersion), codes.Aborted)
	updated, err := client.UpdateGateway(versioned(controller, current.ResourceVersion), update)
	if err != nil || updated.Gateway.GetStatus() != "Healthy" || updated.Gateway.GetExternalDns() != "changed.example.test" {
		t.Fatalf("fresh observation: %v %v", updated, err)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	awaitQueueEmpty(t, f)
	final := observe()
	if final.ResourceVersion != current.ResourceVersion+1 {
		t.Fatal("successful status did not advance revision")
	}
	code, data = requestJSON(t, "GET", httpAddress+"/api/hypershell/v1/gateways/"+gateway.ID, owner, nil)
	if code != 200 || json.Unmarshal(data, &gateway) != nil || gateway.Status == nil || *gateway.Status != "Healthy" {
		t.Fatalf("REST status: %d %s", code, data)
	}
}
