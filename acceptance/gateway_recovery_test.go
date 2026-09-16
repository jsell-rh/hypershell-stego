package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
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

// This API check proves the registration decision across restart. The real
// browser workflow also checks early deletion through all three workers.
func TestGatewayDeletionBeforeWorkloadStartup(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	tlsIdentity := identity(t, "localhost")
	dir := filepath.Dir(tlsIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(dir, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(dir, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["sql-worker","wrong-target","reader"]`)
	foreign := ksuid.New().String()
	settings = withControllerWriteGrants(t, settings, writeGrant("sql-worker", "configure.sql", f.cluster), writeGrant("wrong-target", "configure.sql", foreign))
	settings = withCleanupGrants(t, settings, cleanupGrant("sql-worker", "Gateway", "sql", f.cluster), cleanupGrant("wrong-target", "Gateway", "sql", foreign))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	owner := token(t, key, "owner", "gateway:creator")
	create := func(name string) httpapi.Gateway {
		t.Helper()
		input, _ := json.Marshal(f.request(name))
		code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, input)
		var row httpapi.Gateway
		if code != 201 || json.Unmarshal(body, &row) != nil {
			t.Fatal("Gateway creation failed", code)
		}
		return row
	}
	early, bound := create("deleted-before-work"), create("registered-state")
	_, connection := grpcClient(t, grpcAddress, tlsIdentity)
	client := control.NewGatewayIdentityServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	call := func(subject string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, subject)))
	}
	request := func(id string) *control.GatewaySQLStateRequest {
		return &control.GatewaySQLStateRequest{GatewayId: id, ClusterId: f.cluster}
	}
	revision := func(id string) int64 {
		t.Helper()
		state, err := client.GetGatewayIdentityState(call("sql-worker"), &control.GetGatewayIdentityStateRequest{Id: id})
		if err != nil {
			t.Fatal(err)
		}
		return state.ResourceVersion
	}
	versioned := func(subject string, version int64) context.Context {
		t.Helper()
		c, err := rpc.WithResourceVersion(call(subject), version)
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	digest := strings.Repeat("a", 64)
	bind := &control.BindGatewaySQLStateRequest{GatewayId: bound.ID, ClusterId: f.cluster, Digest: digest}
	before := revision(bound.ID)
	awaitQueueEmpty(t, f)
	for _, subject := range []string{"owner", "reader", "wrong-target"} {
		if _, err := client.LoadGatewaySQLState(call(subject), request(bound.ID)); status.Code(err) != codes.PermissionDenied {
			t.Fatal("SQL state read lacked an exact grant", subject, err)
		}
		if _, err := client.BindGatewaySQLState(versioned(subject, before), bind); status.Code(err) != codes.PermissionDenied {
			t.Fatal("SQL state binding lacked an exact grant", subject, err)
		}
		if _, err := client.CloseGatewaySQLState(call(subject), request(bound.ID)); status.Code(err) != codes.PermissionDenied {
			t.Fatal("SQL state closure lacked an exact grant", subject, err)
		}
	}
	if _, err := client.BindGatewaySQLState(call("sql-worker"), bind); status.Code(err) != codes.FailedPrecondition {
		t.Fatal("binding accepted no resource version", err)
	}
	if _, err := client.BindGatewaySQLState(versioned("sql-worker", before+1), bind); status.Code(err) != codes.Aborted {
		t.Fatal("binding accepted a stale resource version", err)
	}
	for _, bad := range []string{"", strings.Repeat("A", 64), strings.Repeat("a", 63)} {
		bind.Digest = bad
		if _, err := client.BindGatewaySQLState(versioned("sql-worker", before), bind); status.Code(err) != codes.InvalidArgument {
			t.Fatal("invalid binding digest accepted", err)
		}
	}
	bind.Digest = digest
	if _, err := client.CloseGatewaySQLState(call("sql-worker"), request(bound.ID)); status.Code(err) != codes.Aborted {
		t.Fatal("live registration was closed", err)
	}
	if count(t, f.db, "stego_effect_bindings") != 0 {
		t.Fatal("rejected registration wrote state")
	}
	for range 2 {
		value, err := client.BindGatewaySQLState(versioned("sql-worker", before), bind)
		if err != nil || !value.GetPresent() || value.GetClosed() || value.GetDigest() != digest {
			t.Fatal("binding was not retained", err)
		}
	}
	bind.Digest = strings.Repeat("b", 64)
	if _, err := client.BindGatewaySQLState(versioned("sql-worker", before), bind); status.Code(err) != codes.FailedPrecondition {
		t.Fatal("binding replacement succeeded", err)
	}
	bind.Digest = digest
	if revision(bound.ID) != before {
		t.Fatal("state binding changed the public resource revision")
	}
	if n := count(t, f.db, "stego_effect_bindings"); n != 1 {
		t.Fatal("binding record count differs", n)
	}
	consoleDigest := strings.Repeat("c", 64)
	bind.Digest = consoleDigest
	consoleRequest := request(bound.ID)
	for _, subject := range []string{"owner", "reader", "wrong-target"} {
		if _, err := client.LoadGatewayConsoleSQLState(call(subject), consoleRequest); status.Code(err) != codes.PermissionDenied {
			t.Fatal("console SQL read lacked an exact grant", err)
		}
		if _, err := client.BindGatewayConsoleSQLState(versioned(subject, before), bind); status.Code(err) != codes.PermissionDenied {
			t.Fatal("console SQL registration lacked an exact grant", err)
		}
		if _, err := client.CloseGatewayConsoleSQLState(call(subject), consoleRequest); status.Code(err) != codes.PermissionDenied {
			t.Fatal("console SQL closure lacked an exact grant", err)
		}
		if _, err := client.CompleteGatewayConsoleSQLCleanup(call(subject), consoleRequest); status.Code(err) != codes.PermissionDenied {
			t.Fatal("console SQL completion lacked an exact grant", err)
		}
	}
	if _, err := client.CompleteGatewayConsoleSQLCleanup(call("sql-worker"), consoleRequest); status.Code(err) != codes.Aborted {
		t.Fatal("live console cleanup was completed", err)
	}
	for range 2 {
		value, err := client.BindGatewayConsoleSQLState(versioned("sql-worker", before), bind)
		if err != nil || !value.GetPresent() || value.GetClosed() || value.GetDigest() != consoleDigest || value.GetComponent() != control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE {
			t.Fatal("console SQL registration was not isolated", err)
		}
	}
	gatewayState, err := client.LoadGatewaySQLState(call("sql-worker"), request(bound.ID))
	if err != nil || gatewayState.GetDigest() != digest || gatewayState.GetComponent() != control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_GATEWAY {
		t.Fatal("console registration changed Gateway SQL state", err)
	}
	if count(t, f.db, "stego_effect_bindings") != 2 {
		t.Fatal("component registrations do not have separate records")
	}
	for _, row := range []httpapi.Gateway{early, bound} {
		if code, _ := requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+row.ID, owner, nil); code != 202 {
			t.Fatal("Gateway deletion failed", code)
		}
	}
	awaitQueueEmpty(t, f)
	connection.Close()
	stop()
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	defer connection.Close()
	client = control.NewGatewayIdentityServiceClient(connection)
	for _, row := range []httpapi.Gateway{early, bound} {
		for _, component := range []control.GatewaySQLComponent{control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_GATEWAY, control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE} {
			expected := ""
			if row.ID == bound.ID {
				expected = digest
				if component == control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE {
					expected = consoleDigest
				}
			}
			req := request(row.ID)
			load, bindState, closeState := client.LoadGatewaySQLState, client.BindGatewaySQLState, client.CloseGatewaySQLState
			if component == control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE {
				load, bindState, closeState = client.LoadGatewayConsoleSQLState, client.BindGatewayConsoleSQLState, client.CloseGatewayConsoleSQLState
			}
			if component == control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE {
				if _, err := client.CompleteGatewayConsoleSQLCleanup(call("sql-worker"), req); status.Code(err) != codes.FailedPrecondition {
					t.Fatal("open console registration was completed", err)
				}
			}
			for range 2 {
				closed, err := closeState(call("sql-worker"), req)
				if err != nil || !closed.GetPresent() || !closed.GetClosed() || closed.GetDigest() != expected || closed.GetComponent() != component {
					t.Fatal("restart lost component SQL registration history", err)
				}
			}
			bind.GatewayId = row.ID
			if _, err := bindState(versioned("sql-worker", revision(row.ID)), bind); status.Code(err) != codes.NotFound {
				t.Fatal("deleted Gateway accepted component SQL registration", err)
			}
			loaded, err := load(call("sql-worker"), req)
			if err != nil || !loaded.GetClosed() || loaded.GetDigest() != expected || loaded.GetComponent() != component {
				t.Fatal("closed component state changed", err)
			}
			if component == control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_CONSOLE {
				if row.ID == bound.ID {
					_, err := client.ObserveGatewayCleanup(versioned("sql-worker", revision(row.ID)), &control.ObserveGatewayCleanupRequest{Id: row.ID, Owner: "sql", Target: f.cluster, Complete: true})
					if status.Code(err) != codes.FailedPrecondition {
						t.Fatal("registration closure became external cleanup evidence", err)
					}
				}
				for range 2 {
					value, err := client.CompleteGatewayConsoleSQLCleanup(call("sql-worker"), req)
					if err != nil || !value.GetClosed() || value.GetDigest() != expected || value.GetComponent() != component {
						t.Fatal("console cleanup completion was not retained", err)
					}
				}
			}
			if component == control.GatewaySQLComponent_GATEWAY_SQL_COMPONENT_GATEWAY {
				other := request(row.ID)
				state, err := client.LoadGatewayConsoleSQLState(call("sql-worker"), other)
				if err != nil || state.GetClosed() {
					t.Fatal("Gateway closure also closed console registration", err)
				}
			}
		}
	}
	if n := count(t, f.db, "stego_effect_bindings"); n != 6 {
		t.Fatal("component closure record count differs", n)
	}

	connection.Close()
	stop()
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, tlsIdentity)
	defer connection.Close()
	client = control.NewGatewayIdentityServiceClient(connection)
	for _, row := range []httpapi.Gateway{early, bound} {
		if _, err := client.ObserveGatewayCleanup(versioned("sql-worker", revision(row.ID)), &control.ObserveGatewayCleanupRequest{Id: row.ID, Owner: "sql", Target: f.cluster, Complete: true}); err != nil {
			t.Fatal("restart lost console cleanup completion", err)
		}
	}

	t.Log("API restart preserved separate Gateway and console SQL state; late registration and state replacement were denied")
}
