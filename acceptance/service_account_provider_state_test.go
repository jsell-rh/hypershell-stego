package acceptance

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccountprovisioner"
	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestServiceAccountProviderStateAcrossLockAndRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	signing, settings := issuer(t)
	apiTLS := identity(t, "localhost")
	directory := filepath.Dir(apiTLS.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"), `HYPERSHELL_CONTROL_PLANE_SUBJECTS=["account-worker","unassigned"]`)
	settings = withExactGrants(t, "HYPERSHELL_PROVIDER_STATE_GRANTS", settings, auth.Grant{Subject: "account-worker", Resource: "ServiceAccount", Operation: "provider-state"})
	binary := buildApplication(t)
	stop, address, grpcAddress := startBoth(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	_, connection := grpcClient(t, grpcAddress, apiTLS)
	client := control.NewServiceAccountProviderStateServiceClient(connection)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	call := func(subject string, roles ...string) context.Context {
		return metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, signing, subject, roles...)))
	}
	machineToken, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://issuer.example", "aud": "hypershell", "sub": "account-worker", "iat": time.Now().Add(-time.Minute).Unix(), "exp": time.Now().Add(time.Hour).Unix()}).SignedString(signing)
	if err != nil {
		t.Fatal(err)
	}
	worker := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+machineToken))
	owner := token(t, signing, "alice", "gateway:creator")
	createGateway := func(name string) string {
		t.Helper()
		body, _ := json.Marshal(map[string]string{"name": name, "cluster_id": f.cluster, "release_id": f.release})
		code, data := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, body)
		var value httpapi.Gateway
		if code != 201 || json.Unmarshal(data, &value) != nil {
			t.Fatal("Gateway creation failed", code)
		}
		return value.ID
	}
	gatewayID, foreignID := createGateway("account-journal"), createGateway("other-journal")
	accountID := ksuid.New().String()
	result, err := f.db.Exec(`INSERT INTO service_accounts
 (id,created_time,updated_time,gateway_id,name,credential_type,role,status,created_by_user_id,client_id,client_uuid,subject,expires_at,active)
 SELECT $1,now(),now(),$2,'reserved','client_secret','openshell-user','provisioning',id,$3,'','',now()+interval '1 day',true FROM users WHERE username='alice'`, accountID, gatewayID, "hs-sa-"+gatewayID+"-"+accountID)
	if err != nil {
		t.Fatal(err)
	}
	if count, err := result.RowsAffected(); err != nil || count != 1 {
		t.Fatal("account reservation fixture failed", err)
	}
	awaitQueueEmpty(t, f)
	eventSequence := func() int64 {
		t.Helper()
		var n int64
		if err := f.db.QueryRow("SELECT last_value FROM stego_outbox.messages_sequence_seq").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := eventSequence()
	load := func(parent context.Context, gateway, account string, cleanup bool, want codes.Code) *control.ServiceAccountProviderState {
		t.Helper()
		value, err := client.LoadServiceAccountProviderState(parent, &control.LoadServiceAccountProviderStateRequest{GatewayId: gateway, ServiceAccountId: account, Cleanup: cleanup})
		if status.Code(err) != want {
			t.Fatal("account state read", err, "want", want)
		}
		return value
	}
	for _, parent := range []context.Context{call("alice"), call("admin", "platform:admin"), call("unassigned")} {
		load(parent, gatewayID, accountID, false, codes.PermissionDenied)
	}
	load(ctx, gatewayID, accountID, false, codes.Unauthenticated)
	load(worker, foreignID, accountID, true, codes.PermissionDenied)
	orphan := ksuid.New().String()
	load(worker, gatewayID, orphan, false, codes.NotFound)
	load(worker, ksuid.New().String(), orphan, true, codes.NotFound)
	master := make([]byte, 32)
	if _, err = rand.Read(master); err != nil {
		t.Fatal(err)
	}
	protector, err := runtime.NewStateProtector([][]byte{master})
	if err != nil {
		t.Fatal(err)
	}
	journal := func(account string, cleanup bool) *runtime.StateJournal {
		t.Helper()
		j, err := serviceaccountprovisioner.NewProviderStateJournal(client, protector, "account-test", gatewayID, account, cleanup)
		if err != nil {
			t.Fatal(err)
		}
		return j
	}
	j := journal(accountID, false)
	initial, err := j.Load(worker)
	if err != nil || initial.Version() != 0 {
		t.Fatal("initial journal differs", err)
	}
	plain := []byte(`{"client_id":"saved-client","subject":"saved-subject"}`)
	// Hold the same row lock that the API owns during a provisioner call. The
	// journal RPC must commit before that outer transaction releases its lock.
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var locked string
	if err = tx.QueryRowContext(ctx, "SELECT id FROM gateways WHERE id=$1 FOR UPDATE", gatewayID).Scan(&locked); err != nil {
		t.Fatal(err)
	}
	bounded, finish := context.WithTimeout(worker, 3*time.Second)
	saved, saveErr := j.Save(bounded, initial, plain)
	finish()
	rollbackErr := tx.Rollback()
	if saveErr != nil || rollbackErr != nil || saved.Version() != 1 {
		t.Fatal("journal did not commit while the Gateway lock was held", saveErr, rollbackErr)
	}
	if _, err = j.Save(worker, initial, plain); status.Code(err) != codes.Aborted {
		t.Fatal("stale state version accepted", err)
	}

	rawState := load(worker, gatewayID, accountID, false, codes.OK)
	rawSave := func(parent context.Context, request *control.SaveServiceAccountProviderStateRequest, want codes.Code) {
		t.Helper()
		_, err := client.SaveServiceAccountProviderState(parent, request)
		if status.Code(err) != want {
			t.Fatal("raw account journal save", err, "want", want)
		}
	}
	for _, parent := range []context.Context{call("alice"), call("admin", "platform:admin"), call("unassigned")} {
		rawSave(parent, &control.SaveServiceAccountProviderStateRequest{GatewayId: gatewayID, ServiceAccountId: accountID, ExpectedVersion: 1, SealedState: rawState.SealedState}, codes.PermissionDenied)
	}
	rawSave(worker, &control.SaveServiceAccountProviderStateRequest{GatewayId: gatewayID, ServiceAccountId: accountID, ExpectedVersion: -1, SealedState: rawState.SealedState}, codes.InvalidArgument)
	rawSave(worker, &control.SaveServiceAccountProviderStateRequest{GatewayId: gatewayID, ServiceAccountId: accountID, ExpectedVersion: 1, SealedState: []byte("plaintext")}, codes.InvalidArgument)
	rawSave(worker, &control.SaveServiceAccountProviderStateRequest{GatewayId: gatewayID, ServiceAccountId: accountID, ExpectedVersion: 1, SealedState: make([]byte, gateways.MaxGatewayProviderStateBytes+1)}, codes.InvalidArgument)
	unknown := &control.SaveServiceAccountProviderStateRequest{GatewayId: gatewayID, ServiceAccountId: accountID, ExpectedVersion: 1, SealedState: rawState.SealedState}
	unknown.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1})
	rawSave(worker, unknown, codes.InvalidArgument)
	check := func() {
		t.Helper()
		record, err := journal(accountID, false).Load(worker)
		if err != nil || record.Version() != 1 || !bytes.Equal(record.Reveal(), plain) {
			t.Fatal("account journal recovery differs", err)
		}
		raw := load(worker, gatewayID, accountID, false, codes.OK)
		var stored []byte
		if err = f.db.QueryRow("SELECT data FROM stego_resource_state WHERE entity='ServiceAccount' AND resource_id=$1 AND scope=$2", accountID, gateways.AccountProviderStateScope(gatewayID)).Scan(&stored); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(stored, raw.SealedState) || bytes.Contains(stored, plain) {
			t.Fatal("stored account state is not the ciphertext")
		}
		var state, subject, provider string
		if err = f.db.QueryRow("SELECT status,subject,client_uuid FROM service_accounts WHERE id=$1", accountID).Scan(&state, &subject, &provider); err != nil {
			t.Fatal(err)
		}
		if state != "provisioning" || subject != "" || provider != "" || eventSequence() != before {
			t.Fatal("journal changed account metadata or domain events")
		}
	}
	check()
	stop()
	connection.Close()
	stop, address, grpcAddress = startBoth(t, binary, f.dsn, config, settings...)
	_, connection = grpcClient(t, grpcAddress, apiTLS)
	client = control.NewServiceAccountProviderStateServiceClient(connection)
	protector, err = runtime.NewStateProtector([][]byte{bytes.Clone(master)})
	if err != nil {
		t.Fatal(err)
	}
	check()
	// Cleanup retains state even when no account row exists, but only within a
	// retained Gateway and with the separate machine grant.
	orphanJournal := journal(orphan, true)
	empty, err := orphanJournal.Load(worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = orphanJournal.Save(worker, empty, []byte(`{"closed":true}`)); err != nil {
		t.Fatal("orphan cleanup journal failed", err)
	}
	load(worker, gatewayID, orphan, false, codes.NotFound)
	if _, err = f.db.Exec("UPDATE service_accounts SET status='revoked' WHERE id=$1", accountID); err != nil {
		t.Fatal(err)
	}
	load(worker, gatewayID, accountID, false, codes.Aborted)
	cleanupJournal := journal(accountID, true)
	prior, err := cleanupJournal.Load(worker)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = cleanupJournal.Save(worker, prior, []byte(`{"closed":true}`)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Exec("UPDATE gateways SET deleted_at=now() WHERE id=$1", gatewayID); err != nil {
		t.Fatal(err)
	}
	load(worker, gatewayID, accountID, false, codes.Aborted)
	retained := load(worker, gatewayID, accountID, true, codes.OK)
	if retained.Version != 2 {
		t.Fatal("retained cleanup state was lost")
	}
	t.Log("Account provider state passed exact grants, independent commit under a Gateway lock, ciphertext storage, restart, account isolation, and retained orphan cleanup")
}
