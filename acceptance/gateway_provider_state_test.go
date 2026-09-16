package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentity"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	rpc "github.com/jsell-rh/hypershell-stego/out/grpcapi/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGatewayProviderStateAcrossGRPCAndRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	signingKey, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["identity","cleanup","unassigned"]`)
	settings = withControllerWriteGrants(t, settings, writeGrant("identity", "configure.identity", ""))
	settings = withCleanupGrants(t, settings, cleanupGrant("cleanup", "Gateway", "identity", ""))
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	_, connection := grpcClient(t, grpcAddress, apiTLS)
	client := control.NewGatewayIdentityServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	call := func(subject string, roles ...string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, signingKey, subject, roles...)))
	}
	writer, cleaner := call("identity"), call("cleanup")
	owner := token(t, signingKey, "alice", "gateway:creator")
	body, _ := json.Marshal(map[string]string{"name": "protected-provider", "cluster_id": f.cluster, "release_id": f.release})
	code, data := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, body)
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(data, &gateway) != nil {
		t.Fatal("Gateway creation failed", code)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Create", "gateway.created")
	awaitQueueEmpty(t, f)
	load := func(parent context.Context, want codes.Code) *control.GatewayProviderState {
		t.Helper()
		result, err := client.LoadGatewayProviderState(parent, &control.LoadGatewayProviderStateRequest{GatewayId: gateway.ID})
		if status.Code(err) != want {
			t.Fatal("provider state read", err, "want", want)
		}
		return result
	}
	initial := load(writer, codes.OK)
	if initial.GatewayId != gateway.ID || initial.Version != 0 || len(initial.SealedState) != 0 || initial.ResourceVersion != 1 || initial.Deleted {
		t.Fatal("absent provider state differs")
	}
	for _, parent := range []context.Context{call("alice"), call("admin", "platform:admin"), call("unassigned")} {
		load(parent, codes.PermissionDenied)
	}
	load(ctx, codes.Unauthenticated)
	// Request preparation can project global role grants before a denied call.
	// Measure after these calls. A queue count can miss an event already sent.
	eventSequence := func() int64 {
		t.Helper()
		var sequence int64
		if err := f.db.QueryRow("SELECT last_value FROM stego_outbox.messages_sequence_seq").Scan(&sequence); err != nil {
			t.Fatal(err)
		}
		return sequence
	}
	awaitQueueEmpty(t, f)
	lastEvent := eventSequence()
	master := make([]byte, 32)
	if _, err := rand.Read(master); err != nil {
		t.Fatal(err)
	}
	protector, err := runtime.NewStateProtector([][]byte{master})
	if err != nil {
		t.Fatal(err)
	}
	stateKey := runtime.StateKey{Instance: "acceptance-instance", Entity: "Gateway", ResourceID: gateway.ID, Scope: "identity-provider"}
	plain := []byte(`{"provider_id":"saved-provider-id","migration":"private-recovery-checkpoint"}`)
	seal := func(version int64, content []byte) []byte {
		t.Helper()
		result, err := protector.Seal(stateKey, version, content)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	save := func(parent context.Context, revision, expected int64, sealed []byte, cleanup bool, want codes.Code) *control.GatewayProviderState {
		t.Helper()
		if revision > 0 {
			var err error
			parent, err = rpc.WithResourceVersion(parent, revision)
			if err != nil {
				t.Fatal(err)
			}
		}
		result, err := client.SaveGatewayProviderState(parent, &control.SaveGatewayProviderStateRequest{GatewayId: gateway.ID, ExpectedVersion: expected, SealedState: sealed, Cleanup: cleanup})
		if status.Code(err) != want {
			t.Fatal("provider state write", err, "want", want)
		}
		return result
	}
	consoleKind := control.GatewayIdentityClientKind_GATEWAY_IDENTITY_CLIENT_KIND_CONSOLE
	consoleKey := stateKey
	consoleKey.Scope = "console-identity-provider"
	consoleLoad := func(parent context.Context, want codes.Code) *control.GatewayProviderState {
		t.Helper()
		record, err := client.LoadGatewayProviderState(parent, &control.LoadGatewayProviderStateRequest{GatewayId: gateway.ID, ClientKind: consoleKind})
		if status.Code(err) != want {
			t.Fatal("console state read", err, "want", want)
		}
		return record
	}
	consoleSave := func(parent context.Context, revision, expected int64, cleanup bool, want codes.Code) {
		t.Helper()
		parent, err := rpc.WithResourceVersion(parent, revision)
		if err != nil {
			t.Fatal(err)
		}
		sealed, err := protector.Seal(consoleKey, expected+1, []byte("console checkpoint"))
		if err != nil {
			t.Fatal(err)
		}
		record, err := client.SaveGatewayProviderState(parent, &control.SaveGatewayProviderStateRequest{GatewayId: gateway.ID, ClientKind: consoleKind, ExpectedVersion: expected, SealedState: sealed, Cleanup: cleanup})
		if status.Code(err) != want {
			t.Fatal("console state write", err, "want", want)
		}
		if err == nil && (record.ClientKind != consoleKind || record.Version != expected+1) {
			t.Fatal("console write changed scope or version")
		}
	}
	emptyConsole := consoleLoad(writer, codes.OK)
	if emptyConsole.ClientKind != consoleKind || emptyConsole.Version != 0 {
		t.Fatal("console scope is not empty")
	}
	for _, parent := range []context.Context{call("alice"), call("admin", "platform:admin"), call("unassigned")} {
		consoleLoad(parent, codes.PermissionDenied)
		consoleSave(parent, 1, 0, false, codes.PermissionDenied)
	}
	consoleLoad(ctx, codes.Unauthenticated)
	for _, kind := range []control.GatewayIdentityClientKind{-1, 2, 99} {
		if _, err := client.LoadGatewayProviderState(writer, &control.LoadGatewayProviderStateRequest{GatewayId: gateway.ID, ClientKind: kind}); status.Code(err) != codes.InvalidArgument {
			t.Fatal("unknown read scope accepted", err)
		}
		if _, err := client.SaveGatewayProviderState(writer, &control.SaveGatewayProviderStateRequest{GatewayId: gateway.ID, ClientKind: kind}); status.Code(err) != codes.InvalidArgument {
			t.Fatal("unknown write scope accepted", err)
		}
	}
	consoleSave(writer, 1, 0, false, codes.OK)
	verifyConsole := func(version, revision int64, deleted bool) {
		t.Helper()
		record := consoleLoad(cleaner, codes.OK)
		if record.ClientKind != consoleKind || record.Version != version || record.ResourceVersion != revision || record.Deleted != deleted {
			t.Fatal("console state metadata differs")
		}
		opened, err := protector.Open(consoleKey, version, record.SealedState)
		if err != nil || string(opened.Reveal()) != "console checkpoint" {
			t.Fatal("console checkpoint was not retained", err)
		}
		if _, err := protector.Open(stateKey, version, record.SealedState); err == nil {
			t.Fatal("console checkpoint crossed native scope")
		}
	}
	verifyConsole(1, 1, false)
	if record := load(writer, codes.OK); record.Version != 0 {
		t.Fatal("console write changed native version")
	}
	sealed := seal(1, plain)
	for _, parent := range []context.Context{call("alice"), call("admin", "platform:admin"), call("unassigned"), cleaner} {
		save(parent, 1, 0, sealed, false, codes.PermissionDenied)
	}
	save(writer, 0, 0, sealed, false, codes.FailedPrecondition)
	save(writer, 2, 0, sealed, false, codes.Aborted)
	save(writer, 1, -1, sealed, false, codes.InvalidArgument)
	save(writer, 1, 0, plain, false, codes.InvalidArgument)
	save(writer, 1, 0, make([]byte, gateways.MaxGatewayProviderStateBytes+1), false, codes.InvalidArgument)
	save(writer, 1, 0, sealed, true, codes.PermissionDenied)
	save(cleaner, 1, 0, sealed, true, codes.Aborted)
	saved := save(writer, 1, 0, sealed, false, codes.OK)
	if saved.Version != 1 || saved.ResourceVersion != 1 || saved.Deleted || !bytes.Equal(saved.SealedState, sealed) {
		t.Fatal("save response differs")
	}
	verify := func(wantVersion, wantRevision int64, deleted bool, content []byte) {
		t.Helper()
		record := load(cleaner, codes.OK)
		if record.Version != wantVersion || record.ResourceVersion != wantRevision || record.Deleted != deleted {
			t.Fatal("retained metadata differs")
		}
		opened, err := protector.Open(stateKey, record.Version, record.SealedState)
		if err != nil || !bytes.Equal(opened.Reveal(), content) {
			t.Fatal("protected state recovery failed")
		}
		var stored []byte
		var version int64
		if err := f.db.QueryRow("SELECT data,version FROM stego_resource_state WHERE entity='Gateway' AND resource_id=$1 AND scope='identity-provider'", gateway.ID).Scan(&stored, &version); err != nil {
			t.Fatal(err)
		}
		if version != wantVersion || !bytes.Equal(stored, record.SealedState) || bytes.Contains(stored, plain) {
			t.Fatal("database state is not the protected record")
		}
		if eventSequence() != lastEvent {
			t.Fatal("provider state write created a domain event")
		}
	}
	verify(1, 1, false, plain)
	verifyConsole(1, 1, false)
	save(writer, 1, 0, sealed, false, codes.Aborted)
	// Restart uses the same database and an independent copy of the controller key.
	stop()
	connection.Close()
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, apiTLS)
	client = control.NewGatewayIdentityServiceClient(connection)
	protector, err = runtime.NewStateProtector([][]byte{bytes.Clone(master)})
	if err != nil {
		t.Fatal(err)
	}
	verify(1, 1, false, plain)
	verifyConsole(1, 1, false)
	// A desired-state change invalidates a pending state save.
	code, _ = requestJSON(t, "PATCH", address+"/api/hypershell/v1/gateways/"+gateway.ID, owner, []byte(`{"name":"changed-provider-input"}`))
	if code != 200 {
		t.Fatal("Gateway update failed", code)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	awaitQueueEmpty(t, f)
	lastEvent = eventSequence()
	next := seal(2, plain)
	save(writer, 1, 1, next, false, codes.Aborted)
	save(writer, 2, 1, next, false, codes.OK)
	verify(2, 2, false, plain)
	code, _ = requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+gateway.ID, owner, nil)
	if code != 202 {
		t.Fatal("Gateway deletion failed", code)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	awaitQueueEmpty(t, f)
	lastEvent = eventSequence()
	verify(2, 3, true, plain)
	verifyConsole(1, 3, true)
	consoleSave(writer, 3, 1, false, codes.NotFound)
	consoleSave(writer, 3, 1, true, codes.PermissionDenied)
	consoleSave(cleaner, 2, 1, true, codes.Aborted)
	consoleSave(cleaner, 3, 0, true, codes.Aborted)
	consoleSave(cleaner, 3, 1, true, codes.OK)
	verifyConsole(2, 3, true)
	closed := []byte(`{"provider_id":"saved-provider-id","closed":true}`)
	last := seal(3, closed)
	save(writer, 3, 2, last, false, codes.NotFound)
	save(writer, 3, 2, last, true, codes.PermissionDenied)
	save(cleaner, 2, 2, last, true, codes.Aborted)
	save(cleaner, 3, 1, last, true, codes.Aborted)
	save(cleaner, 3, 2, last, true, codes.OK)
	verify(3, 3, true, closed)
	// The largest application record must fit both generated RPC directions.
	overhead := len(seal(4, nil))
	maximum := bytes.Repeat([]byte{'s'}, gateways.MaxGatewayProviderStateBytes-overhead)
	maximumSealed := seal(4, maximum)
	if len(maximumSealed) != gateways.MaxGatewayProviderStateBytes {
		t.Fatal("record boundary differs")
	}
	save(cleaner, 3, 3, maximumSealed, true, codes.OK)
	verify(4, 3, true, maximum)
	verifyConsole(2, 3, true)
	stop()
	connection.Close()
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, apiTLS)
	client = control.NewGatewayIdentityServiceClient(connection)
	verify(4, 3, true, maximum)
	verifyConsole(2, 3, true)
	// The same private API supplies the common encrypted journal after restart.
	journal, err := gatewayidentity.NewProviderStateJournal(client, protector, stateKey.Instance, gateway.ID, 3, true)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := journal.Load(cleaner)
	if err != nil || snapshot.Version() != 4 || !bytes.Equal(snapshot.Reveal(), maximum) {
		t.Fatal("common journal did not recover the largest record", err)
	}
	updated, err := journal.Save(cleaner, snapshot, closed)
	if err != nil || updated.Version() != 5 || !bytes.Equal(updated.Reveal(), closed) {
		t.Fatal("common journal save failed", err)
	}
	verify(5, 3, true, closed)
	if _, err := journal.Save(cleaner, snapshot, closed); status.Code(err) != codes.Aborted {
		t.Fatal("common journal accepted a stale snapshot", err)
	}
	stale, err := gatewayidentity.NewProviderStateJournal(client, protector, stateKey.Instance, gateway.ID, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stale.Load(cleaner); status.Code(err) != codes.Aborted {
		t.Fatal("common journal accepted a stale resource observation", err)
	}
	save(cleaner, 3, 2, last, true, codes.Aborted)
	requireDeletingGateway(t, address+"/api/hypershell/v1/gateways/"+gateway.ID, owner)
	t.Log("REST resource revisions, private TLS gRPC permissions, protected database state, API restart, and retained cleanup passed")
}
