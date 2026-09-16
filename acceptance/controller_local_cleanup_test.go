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
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestControllerLocalSQLCleanupRequiresExactGrantAndVersion(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	directory := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["sql-worker","workload-worker","wrong-target"]`)
	settings = withCleanupGrants(t, settings, cleanupGrant("sql-worker", "Gateway", "sql", f.cluster), cleanupGrant("workload-worker", "Gateway", "workload", f.cluster), cleanupGrant("wrong-target", "Gateway", "sql", ksuid.New().String()))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	owner := token(t, key, "owner", "gateway:creator")
	body, _ := json.Marshal(f.request("sql-cleanup"))
	code, data := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, body)
	var row httpapi.Gateway
	if code != 201 || json.Unmarshal(data, &row) != nil {
		t.Fatal("Gateway creation failed", code)
	}
	if code, _ := requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+row.ID, owner, nil); code != 202 {
		t.Fatal("Gateway deletion failed", code)
	}
	_, connection := grpcClient(t, grpcAddress, tlsIdentity)
	client := control.NewGatewayIdentityServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	call := func(subject string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, subject)))
	}
	read := func() *control.GetGatewayIdentityStateResponse {
		t.Helper()
		state, err := client.GetGatewayIdentityState(call("sql-worker"), &control.GetGatewayIdentityStateRequest{Id: row.ID})
		if err != nil || !state.GetDeleted() {
			t.Fatal("retained cleanup state is unavailable", err)
		}
		return state
	}
	original := read()
	sqlTargets := original.GetCleanupTargets()["sql"].GetTargets()
	complete, found := sqlTargets[f.cluster]
	if !found || complete || len(sqlTargets) != 1 {
		t.Fatal("SQL target was not recorded at creation")
	}
	observe := func(subject string, version int64, want codes.Code) {
		t.Helper()
		write, err := rpc.WithResourceVersion(call(subject), version)
		if err != nil {
			t.Fatal(err)
		}
		_, err = client.ObserveGatewayCleanup(write, &control.ObserveGatewayCleanupRequest{Id: row.ID, Owner: "sql", Target: f.cluster, Complete: true})
		if status.Code(err) != want {
			t.Fatal("SQL cleanup result differs", status.Code(err), want)
		}
	}
	for _, subject := range []string{"workload-worker", "wrong-target"} {
		observe(subject, original.ResourceVersion, codes.PermissionDenied)
		if current := read(); current.ResourceVersion != original.ResourceVersion || current.GetCleanupTargets()["sql"].GetTargets()[f.cluster] {
			t.Fatal("denied cleanup changed state")
		}
	}
	observe("sql-worker", original.ResourceVersion, codes.OK)
	completeState := read()
	if !completeState.GetCleanupTargets()["sql"].GetTargets()[f.cluster] || completeState.GetCleanupTargets()["workload"].GetTargets()[f.cluster] || completeState.ResourceVersion <= original.ResourceVersion {
		t.Fatal("SQL cleanup changed workload completion")
	}
	observe("sql-worker", original.ResourceVersion, codes.Aborted)
	awaitQueueEmpty(t, f)
	stop()
	connection.Close()
	stop, _, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	client = control.NewGatewayIdentityServiceClient(connection)
	if current := read(); current.ResourceVersion != completeState.ResourceVersion || !current.GetCleanupTargets()["sql"].GetTargets()[f.cluster] || current.GetCleanupTargets()["workload"].GetTargets()[f.cluster] {
		t.Fatal("restart lost separate cleanup records")
	}
}
