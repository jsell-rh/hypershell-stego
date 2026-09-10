package acceptance

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestIdentityCycleInvalidatesWithGrantAndEvent(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("grant-cycle"))
	if err != nil {
		t.Fatal(err)
	}
	var cursor string
	if err := f.db.QueryRow("SELECT id FROM role_bindings WHERE gateway_id=$1 ORDER BY id LIMIT 1", gateway.ID).Scan(&cursor); err != nil {
		t.Fatal(err)
	}
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["controller","observer"]`, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	settings = withControllerWriteGrants(t, settings, writeGrant("controller", "configure.identity", ""))
	binary := buildApplication(t)
	stopAPI, address, rpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stopAPI() }()
	_, connection := grpcClient(t, rpcAddress, apiTLS)
	client := control.NewGatewayIdentityServiceClient(connection)
	auth := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "controller")))
	load := func() *control.GatewayIdentityCycle {
		t.Helper()
		value, err := client.LoadGatewayIdentityCycle(auth, &control.LoadGatewayIdentityCheckpointRequest{GatewayId: gateway.ID})
		if err != nil {
			t.Fatal(err)
		}
		return value
	}
	save := func(before *control.GatewayIdentityCycle, failed, complete bool) (*control.GatewayIdentityCycle, error) {
		data, err := runtime.EncodeCycle(runtime.CycleState{Source: strconv.FormatInt(before.ResourceGeneration, 10), After: cursor, Failed: failed, Complete: complete})
		if err != nil {
			t.Fatal(err)
		}
		return client.SaveGatewayIdentityCycle(auth, &control.SaveGatewayIdentityCycleRequest{GatewayId: gateway.ID, ExpectedVersion: before.Version, ResourceGeneration: before.ResourceGeneration, ResourceVersion: before.ResourceVersion, Data: data})
	}
	readCondition := func() *control.ResourceCondition {
		t.Helper()
		state, err := client.GetGatewayIdentityState(auth, &control.GetGatewayIdentityStateRequest{Id: gateway.ID})
		if err != nil {
			t.Fatal(err)
		}
		return state.GetConditions()["identity_users"].GetConditions()["GrantsSynchronized"]
	}
	readGatewayEvent(t, consumer, gateway.ID, "Create", "gateway.created")
	initial := load()
	if initial.Data != "" || initial.Version != 0 {
		t.Fatal("new cycle has evidence", initial)
	}
	failed, err := save(initial, true, false)
	if err != nil {
		t.Fatal(err)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	if value := readCondition(); value.GetStatus() != "Unknown" || value.GetReason() != "GrantSyncIncomplete" {
		t.Fatal("failed cycle has no condition", value)
	}
	if _, err := save(failed, false, true); status.Code(err) != codes.InvalidArgument {
		t.Fatal("earlier failure was cleared", err)
	}
	stopAPI()
	stopAPI, address, rpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, rpcAddress, apiTLS)
	client = control.NewGatewayIdentityServiceClient(connection)
	restarted := load()
	if restarted.Data != failed.Data || restarted.Version != failed.Version {
		t.Fatal("restart lost failure", restarted)
	}
	complete, err := save(restarted, true, true)
	if err != nil {
		t.Fatal(err)
	}
	ready, err := save(complete, false, true)
	if err != nil {
		t.Fatal(err)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	if value := readCondition(); value.GetStatus() != "True" || !value.GetCurrent() {
		t.Fatal("clean cycle has no condition", value)
	}
	for _, revision := range []int64{0, -1, ready.ResourceVersion - 1} {
		expected := codes.Aborted
		if revision < 1 {
			expected = codes.FailedPrecondition
		}
		if _, err := client.SaveGatewayIdentityCycle(auth, &control.SaveGatewayIdentityCycleRequest{GatewayId: gateway.ID, ExpectedVersion: ready.Version, ResourceGeneration: ready.ResourceGeneration, ResourceVersion: revision, Data: ready.Data}); status.Code(err) != expected {
			t.Fatal("missing or old resource revision accepted", revision, err)
		}
	}
	for _, subject := range []string{"alice", "observer"} {
		denied := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, subject)))
		if _, err := client.LoadGatewayIdentityCycle(denied, &control.LoadGatewayIdentityCheckpointRequest{GatewayId: gateway.ID}); status.Code(err) != codes.PermissionDenied {
			t.Fatal("denied cycle read accepted", subject, err)
		}
		if _, err := client.SaveGatewayIdentityCycle(denied, &control.SaveGatewayIdentityCycleRequest{GatewayId: gateway.ID, ExpectedVersion: ready.Version, ResourceGeneration: ready.ResourceGeneration, ResourceVersion: ready.ResourceVersion, Data: ready.Data}); status.Code(err) != codes.PermissionDenied {
			t.Fatal("denied cycle write accepted", subject, err)
		}
	}
	input := grantInput(t, f, gateway.ID, "bob", "gateway:viewer")
	body, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	owner := token(t, key, "alice")
	base := address + "/api/hypershell/v1"
	// The first grant event can insert. The later Gateway event fails after the
	// cycle reset, so rollback must preserve both the grant set and cycle evidence.
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_cycle_event CHECK(kind <> 'gateway.updated') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	if _, err := save(ready, true, false); err == nil {
		t.Fatal("condition event failure was ignored")
	}
	if unchanged := load(); unchanged.Version != ready.Version || unchanged.ResourceVersion != ready.ResourceVersion || unchanged.Data != ready.Data || readCondition().GetStatus() != "True" {
		t.Fatal("event failure committed partial cycle or condition", unchanged)
	}
	if code, _ := requestJSON(t, "POST", base+"/role_bindings", owner, body); code != 500 {
		t.Fatal("event failure was ignored", code)
	}
	if unchanged := load(); unchanged.Version != ready.Version || unchanged.Data != ready.Data || unchanged.ResourceVersion != ready.ResourceVersion || readCondition().GetStatus() != "True" {
		t.Fatal("event failure committed cycle reset", unchanged)
	}
	var grants int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE gateway_id=$1 AND user_id=$2 AND deleted_at IS NULL", gateway.ID, input.UserID).Scan(&grants); err != nil || grants != 0 {
		t.Fatal("event failure committed grant", grants, err)
	}
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_cycle_event"); err != nil {
		t.Fatal(err)
	}
	code, response := requestJSON(t, "POST", base+"/role_bindings", owner, body)
	var grant struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(response, &grant) != nil || grant.ID == "" {
		t.Fatal("grant create failed", code)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	reset := load()
	if value := readCondition(); value.GetStatus() != "Unknown" || value.GetReason() != "GrantsChanged" || !value.GetCurrent() {
		t.Fatal("grant change kept positive condition", value)
	}
	if reset.Data != "" || reset.Version != ready.Version+1 || reset.ResourceGeneration != ready.ResourceGeneration {
		t.Fatal("grant did not invalidate cycle evidence", reset)
	}
	if _, err := save(ready, false, true); status.Code(err) != codes.Aborted {
		t.Fatal("old cycle restored after grant change", err)
	}
	if code, _ := requestJSON(t, "DELETE", base+"/role_bindings/"+grant.ID, owner, nil); code != 204 {
		t.Fatal("grant delete failed", code)
	}
	readGatewayEvent(t, consumer, gateway.ID, "Update", "gateway.updated")
	deletedGrant := load()
	if deletedGrant.Data != "" || deletedGrant.Version != reset.Version+1 {
		t.Fatal("deletion did not advance invalidation", deletedGrant)
	}
	if deletedGrant.ResourceVersion != reset.ResourceVersion {
		t.Fatal("unchanged invalidation rewrote the condition")
	}
	if _, err := save(reset, false, true); status.Code(err) != codes.Aborted {
		t.Fatal("checkpoint-only invalidation accepted a stale scan", err)
	}
	if code, _ := requestJSON(t, "PATCH", base+"/gateways/"+gateway.ID, owner, []byte(`{"name":"changed-cycle-input"}`)); code != 200 {
		t.Fatal("desired change failed", code)
	}
	if _, err := save(deletedGrant, false, true); status.Code(err) != codes.Aborted {
		t.Fatal("old desired generation was accepted", err)
	}
	current := load()
	if value := readCondition(); value.GetCurrent() || value.GetStatus() != "Unknown" || value.GetReason() != "ObservationPending" {
		t.Fatal("old generation kept current grant condition", value)
	}
	if current.Data != "" || current.Version != deletedGrant.Version {
		t.Fatal("old generation exposed cycle evidence", current)
	}
	if err := f.service.Delete(ctx, principal("alice"), gateway.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := save(current, false, true); status.Code(err) != codes.NotFound {
		t.Fatal("deleted Gateway accepted a cycle", err)
	}
	t.Log("Cycle failure, restart, grants, denied calls, atomic event rollback, desired generation, and deletion passed")
}
