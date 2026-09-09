package acceptance

import (
	"context"
	"encoding/json"
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
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["workload","identity","second","ungranted","console","cleanup"]`)
	settings = withCleanupGrants(t, settings, cleanupGrant("cleanup", "Gateway", "workload", f.cluster))
	settings = withControllerWriteGrants(t, settings, writeGrant("workload", "observe.workload", f.cluster), writeGrant("identity", "configure.identity", ""), writeGrant("second", "observe.workload", second), writeGrant("console", "configure.console", f.cluster), writeGrant("ordinary", "observe.workload", f.cluster))
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
	root := address + "/api/hypershell/v1/gateways/" + row.ID
	before := state()
	if code, _ := requestJSON(t, "PATCH", root, workloadToken, []byte(`{"oidc":"wrong"}`)); code != 428 {
		t.Fatal("REST controller bypass", code)
	}
	if !reflect.DeepEqual(before, state()) {
		t.Fatal("REST denial changed state")
	}
	patch, _ := json.Marshal(map[string]string{"cluster_id": second})
	if code, data := requestJSON(t, "PATCH", root, token(t, key, "alice"), patch); code != 200 {
		t.Fatal("owner move", code, string(data))
	}
	readGatewayEvent(t, consumer, row.ID, "Update", "gateway.updated")
	awaitQueueEmpty(t, f)
	write(workloadToken, statusPatch(), codes.PermissionDenied)
	write(token(t, key, "console"), &pb.UpdateGatewayRequest{ConsoleAddress: pointer("https://wrong.example")}, codes.PermissionDenied)
	secondToken := token(t, key, "second")
	write(secondToken, statusPatch(), codes.OK)
	connection.Close()
	stop()
	settings = withControllerWriteGrants(t, settings, writeGrant("identity", "configure.identity", ""))
	stop, _, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	client, _ = grpcClient(t, grpcAddress, tlsIdentity)
	write(secondToken, statusPatch(), codes.PermissionDenied)
	write(token(t, key, "identity"), &pb.UpdateGatewayRequest{Oidc: pointer(`{"issuer":"updated"}`)}, codes.OK)
}
