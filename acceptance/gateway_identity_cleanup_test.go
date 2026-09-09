package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGatewayIdentityCleanupIsAtomicAndSurvivesRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller"]`)
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	_, connection := grpcClient(t, grpcAddress, tlsIdentity)
	cleanup := control.NewGatewayIdentityServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin := token(t, key, "admin", "platform:admin")
	owner := token(t, key, "alice", "gateway:creator")
	call := func(bearer string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer))
	}
	controller := call(token(t, key, "controller"))
	root := address + "/api/hypershell/v1/gateways"
	body, _ := json.Marshal(map[string]string{"name": "cleanup", "cluster_id": f.cluster, "release_id": f.release, "database_id": f.database})
	code, data := requestJSON(t, "POST", root, owner, body)
	var row httpapi.Gateway
	if code != 201 || json.Unmarshal(data, &row) != nil {
		t.Fatalf("create: %d %s", code, data)
	}
	readEvent(t, consumer, row.ID)
	awaitQueueEmpty(t, f)
	read := func(wantDeleted, wantComplete bool, wantVersion int64) {
		t.Helper()
		response, err := cleanup.GetGatewayIdentityState(controller, &control.GetGatewayIdentityStateRequest{Id: row.ID})
		if err != nil {
			t.Fatal(err)
		}
		complete, declared := response.Cleanup["identity"]
		if response.ResourceVersion != wantVersion || response.Deleted != wantDeleted || !declared || complete != wantComplete || response.Gateway.GetMetadata().GetId() != row.ID {
			t.Fatal("invalid Gateway cleanup state", response)
		}
	}
	observe := func(parent context.Context, version int64, owner string, complete bool, want codes.Code) {
		t.Helper()
		if version > 0 {
			var err error
			parent, err = rpc.WithResourceVersion(parent, version)
			if err != nil {
				t.Fatal(err)
			}
		}
		_, err := cleanup.ObserveGatewayCleanup(parent, &control.ObserveGatewayCleanupRequest{Id: row.ID, Owner: owner, Complete: complete})
		if status.Code(err) != want {
			t.Fatal("cleanup result", err, "want", want)
		}
	}
	read(false, false, 1)
	observe(controller, 1, "identity", true, codes.Aborted)
	if code, _ := requestJSON(t, "DELETE", root+"/"+row.ID, owner, nil); code != 204 {
		t.Fatal("delete", code)
	}
	readGatewayEvent(t, consumer, row.ID, "Delete", "gateway.deleted")
	awaitQueueEmpty(t, f)
	read(true, false, 2)
	observe(call(admin), 2, "identity", true, codes.PermissionDenied)
	observe(call(token(t, key, "outsider")), 2, "identity", true, codes.PermissionDenied)
	observe(controller, 2, "other", true, codes.PermissionDenied)
	observe(controller, 0, "identity", true, codes.FailedPrecondition)
	observe(metadata.AppendToOutgoingContext(controller, "if-resource-version", "01"), 0, "identity", true, codes.InvalidArgument)
	observe(controller, 1, "identity", true, codes.Aborted)
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_cleanup_event CHECK(false) NOT VALID"); err != nil {
		t.Fatal(err)
	}
	observe(controller, 2, "identity", true, codes.Internal)
	read(true, false, 2)
	var queued int
	if err := f.db.QueryRow("SELECT count(*) FROM stego_outbox.messages WHERE resource_key=$1 AND kind='gateway.deleted'", row.ID).Scan(&queued); err != nil || queued != 0 {
		t.Fatal("rejected cleanup committed an event", queued, err)
	}
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_cleanup_event"); err != nil {
		t.Fatal(err)
	}
	observe(controller, 2, "identity", true, codes.OK)
	readGatewayEvent(t, consumer, row.ID, "Delete", "gateway.deleted")
	awaitQueueEmpty(t, f)
	read(true, true, 3)
	stop()
	connection.Close()
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	cleanup = control.NewGatewayIdentityServiceClient(connection)
	read(true, true, 3)
	observe(controller, 2, "identity", false, codes.Aborted)
	observe(controller, 3, "identity", false, codes.OK)
	readGatewayEvent(t, consumer, row.ID, "Delete", "gateway.deleted")
	awaitQueueEmpty(t, f)
	read(true, false, 4)
	observe(controller, 4, "identity", true, codes.OK)
	readGatewayEvent(t, consumer, row.ID, "Delete", "gateway.deleted")
	awaitQueueEmpty(t, f)
	read(true, true, 5)
	// Retained input changes require a fresh absence observation.
	if _, err := f.db.Exec("UPDATE gateways SET name='changed-cleanup-input' WHERE id=$1", row.ID); err != nil {
		t.Fatal(err)
	}
	read(true, false, 6)
	observe(controller, 5, "identity", true, codes.Aborted)
	if code, _ := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways/"+row.ID, owner, nil); code != 404 {
		t.Fatal("cleanup changed public deletion visibility", code)
	}
}
