package acceptance

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	web "github.com/jsell-rh/hypershell-stego/out/application/client"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

// This gate measures account cleanup, not Gateway workload or database cleanup.
// Background rows and provider clients are seeded. The selected Gateway's 100
// accounts must pass the real REST and provisioner creation path before timing.
func TestRealProviderAccountCapacity(t *testing.T) {
	if os.Getenv("STEGO_CAPACITY_CI") != "1" {
		t.Skip("run the bounded provider capacity workflow in CI")
	}
	if raceEnabled || os.Getenv("STEGO_CAPACITY_BIN_DIR") == "" || os.Getenv("STEGO_CAPACITY_RESULT") == "" {
		t.Fatal("capacity requires prebuilt ordinary binaries and a result path")
	}
	const gatewaysCount, perGateway = 100, 100
	record := map[string]any{"schema": 1, "scope": "account_cleanup", "stage": "setup", "gateways": gatewaysCount, "accounts_per_gateway": perGateway, "seeded_background_accounts": 9900, "rest_created_accounts": 0, "target_seconds": 30, "target_met": false, "complete": false, "keycloak_mode": "start", "keycloak_database": "postgresql", "workload_cleanup_tested": false, "background_creation_tested": false, "race_instrumented": false}
	t.Cleanup(func() {
		record["test_passed"] = !t.Failed()
		data, err := json.MarshalIndent(record, "", "  ")
		if err == nil {
			err = os.WriteFile(os.Getenv("STEGO_CAPACITY_RESULT"), append(data, '\n'), 0600)
		}
		if err != nil {
			t.Error("save capacity result", err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 14*time.Minute)
	defer cancel()
	f := database(t)
	providerDB := database(t) // Separate disposable database; no shared tables.
	gatewaysIDs := make([]string, gatewaysCount)
	for i := range gatewaysIDs {
		row, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request(fmt.Sprintf("capacity-%03d", i)))
		if err != nil {
			t.Fatal(err)
		}
		gatewaysIDs[i] = row.ID
	}
	selected := gatewaysIDs[0]
	var creator string
	if err := f.db.QueryRowContext(ctx, "SELECT id FROM users WHERE username='alice'").Scan(&creator); err != nil {
		t.Fatal(err)
	}
	background := make([]capacityBackgroundAccount, 0, (gatewaysCount-1)*perGateway)
	for _, gateway := range gatewaysIDs[1:] {
		for range perGateway {
			id := ksuid.New().String()
			background = append(background, capacityBackgroundAccount{gateway: gateway, account: id, name: "hs-sa-" + gateway + "-" + id})
		}
	}
	k := startKeycloakWithDatabase(t, "127.0.0.1", func(realm map[string]any) {
		clients := realm["clients"].([]any)
		roles := realm["roles"].(map[string]any)["client"].(map[string]any)
		for _, gateway := range gatewaysIDs[1:] {
			audience, err := keycloak.GatewayClientID(gateway)
			if err != nil {
				t.Fatal(err)
			}
			clients = append(clients, map[string]any{"clientId": audience, "enabled": true, "publicClient": false, "standardFlowEnabled": false, "attributes": map[string]string{"hypershell.gateway": "true", "hypershell.gateway-id": gateway}})
			roles[audience] = []any{map[string]any{"name": "openshell-user"}, map[string]any{"name": "openshell-admin"}}
		}
		realm["clients"] = clients
	}, providerDB.dsn)
	t.Log("PostgreSQL-backed Keycloak has started")
	seedCapacityAccounts(t, ctx, k, background)
	for _, row := range background {
		name := row.account
		account := model.ServiceAccount{Meta: model.Meta{ID: row.account}, GatewayID: row.gateway, Name: name, ActiveName: &name, Active: true, CredentialType: "client_secret", Role: serviceaccounts.RoleUser, Status: "ready", CreatedByUserID: creator, ClientID: row.name, ClientUuid: row.client, Subject: row.subject, ExpiresAt: time.Now().Add(time.Hour)}
		if err := f.storage.Create(ctx, "ServiceAccount", account); err != nil {
			t.Fatal(err)
		}
	}
	k.bindGateway(t, "gateway-audience", selected)
	oidc := fmt.Sprintf(`{"issuer":%q,"client_id":"gateway-audience","audience":"gateway-audience"}`, k.options.ServerURL+"/realms/workflow")
	if _, err := f.db.ExecContext(ctx, "UPDATE gateways SET oidc=$1 WHERE id=$2", oidc, selected); err != nil {
		t.Fatal(err)
	}
	observeGatewayFixture(t, f, selected)
	key, settings := issuer(t)
	providerSettings, _ := startRealProvisioner(t, f, k, key, settings)
	_, config := broker(t, identity(t, "localhost"))
	_, address := startApplication(t, buildApplication(t), f.dsn, config, append(settings, providerSettings...)...)
	owner := token(t, key, "alice")
	path := address + "/api/hypershell/v1/gateways/" + selected
	record["stage"] = "account_creation"
	for i := range perGateway {
		body, _ := json.Marshal(map[string]string{"name": fmt.Sprintf("capacity-%03d", i)})
		code, _ := requestJSON(t, http.MethodPost, path+"/service_accounts", owner, body)
		if code != http.StatusCreated {
			t.Fatal("real account creation failed", i, code)
		}
		record["rest_created_accounts"] = i + 1
	}
	rows, err := f.db.QueryContext(ctx, "SELECT id,client_uuid,client_id,subject FROM service_accounts WHERE gateway_id=$1 ORDER BY id", selected)
	if err != nil {
		t.Fatal(err)
	}
	type accountIdentity struct{ ID, Provider, Name, Subject string }
	selectedAccounts := []accountIdentity{}
	for rows.Next() {
		var item accountIdentity
		if err := rows.Scan(&item.ID, &item.Provider, &item.Name, &item.Subject); err != nil {
			t.Fatal(err)
		}
		selectedAccounts = append(selectedAccounts, item)
	}
	rowErr := rows.Err()
	rows.Close()
	if rowErr != nil || len(selectedAccounts) != perGateway {
		t.Fatal("incomplete account fixture", rowErr)
	}
	var bearer string
	var refresh time.Time
	admin := func(method, path string) web.Response {
		t.Helper()
		if time.Now().After(refresh) {
			response, grant := k.issue(t, "provisioner", "acceptance-only-admin-secret")
			var ok bool
			bearer, ok = grant["access_token"].(string)
			if response.StatusCode != http.StatusOK || !ok || bearer == "" {
				t.Fatal("capacity administrator login failed")
			}
			refresh = time.Now().Add(time.Minute)
		}
		response, err := k.http.Do(ctx, method, "/admin/realms/workflow"+path, http.Header{"Authorization": {"Bearer " + bearer}}, nil)
		if err != nil {
			t.Fatal("capacity provider check failed", err)
		}
		return response
	}
	providerSnapshot := func() map[string]string {
		t.Helper()
		result := map[string]string{}
		for first := 0; first <= 10100; first += 100 {
			response := admin(http.MethodGet, fmt.Sprintf("/clients?first=%d&max=100", first))
			var page []json.RawMessage
			if response.StatusCode != 200 || json.Unmarshal(response.Body, &page) != nil {
				t.Fatal("capacity provider list failed", response.StatusCode)
			}
			for _, raw := range page {
				snapshot, valid := clientSnapshot(raw)
				id, _ := snapshot["id"].(string)
				if !valid || id == "" || result[id] != "" {
					t.Fatal("capacity provider page is invalid")
				}
				canonical, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				sum := sha256.Sum256(canonical)
				result[id] = hex.EncodeToString(sum[:])
			}
			if len(page) < 100 {
				return result
			}
		}
		t.Fatal("capacity provider snapshot exceeded its fixture bound")
		return nil
	}
	before := providerSnapshot()
	for _, row := range background {
		if before[row.client] == "" {
			t.Fatal("seeded background client is absent")
		}
	}
	for _, row := range selectedAccounts {
		if before[row.Provider] == "" {
			t.Fatal("created client is absent")
		}
		delete(before, row.Provider)
	}
	beforeSQL := capacityBackgroundSnapshot(t, ctx, f, selected)
	var total, ready int
	if err := f.db.QueryRowContext(ctx, "SELECT count(*),count(*) FILTER (WHERE status='ready' AND active AND deleted_at IS NULL) FROM service_accounts").Scan(&total, &ready); err != nil || total != 10000 || ready != total {
		t.Fatal("capacity account cardinality is invalid", total, ready, err)
	}
	record["sql_accounts_before"], record["provider_account_clients_before"] = total, len(background)+len(selectedAccounts)
	record["stage"] = "cleanup"
	t.Log("Start REST deletion with 100 Gateways and 10,000 account rows and clients")
	if code, _ := requestJSON(t, http.MethodDelete, path, owner, nil); code != http.StatusAccepted {
		t.Fatal("Gateway deletion was not accepted", code)
	}
	accepted := time.Now()
	if code, _ := requestJSON(t, http.MethodPost, path+"/service_accounts", owner, []byte(`{"name":"blocked"}`)); code != http.StatusNotFound {
		t.Fatal("deleting Gateway accepted a new account", code)
	}
	scope := gateways.AccountProviderStateScope(selected)
	for {
		membership, err := f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scope)
		if err != nil {
			t.Fatal(err)
		}
		if membership.Sealed {
			break
		}
		if time.Since(accepted) > 2*time.Minute || ctx.Err() != nil {
			record["elapsed_seconds"] = time.Since(accepted).Seconds()
			t.Fatal("account cleanup did not complete within the observation limit")
		}
		time.Sleep(100 * time.Millisecond)
	}
	record["scope_sealed_seconds"] = time.Since(accepted).Seconds()
	for _, row := range selectedAccounts {
		for _, providerPath := range []string{"/clients/" + row.Provider, "/users/" + row.Subject} {
			if response := admin(http.MethodGet, providerPath); response.StatusCode != http.StatusNotFound {
				t.Fatal("selected provider identity survived cleanup", response.StatusCode)
			}
		}
	}
	rawKeys, err := os.ReadFile(k.stateKeysFile)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := runtime.NewStateProtectorFromJSON(rawKeys)
	clear(rawKeys)
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range selectedAccounts {
		sealed, err := f.storage.LoadResourceState(ctx, "ServiceAccount", account.ID, scope)
		if err != nil {
			t.Fatal(err)
		}
		state, err := protector.Open(runtime.StateKey{Instance: k.instanceID, Entity: "ServiceAccount", ResourceID: account.ID, Scope: scope}, sealed.Version, sealed.Data)
		if err != nil {
			t.Fatal("closed account journal did not authenticate")
		}
		plain := state.Reveal()
		var journal struct {
			Closed  bool
			Binding struct{ ID, ClientID string }
		}
		err = json.Unmarshal(plain, &journal)
		clear(plain)
		if err != nil || !journal.Closed || journal.Binding.ID != account.Provider || journal.Binding.ClientID != account.Name {
			t.Fatal("account cleanup did not close its protected identity")
		}
	}
	var closed, audits int
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM service_accounts WHERE gateway_id=$1 AND deleted_at IS NOT NULL AND NOT active", selected).Scan(&closed); err != nil || closed != perGateway {
		t.Fatal("selected account metadata is not closed", closed, err)
	}
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM service_account_audits WHERE gateway_id=$1 AND action='gateway_cleanup' AND outcome='succeeded'", selected).Scan(&audits); err != nil || audits != perGateway {
		t.Fatal("selected account cleanup audits are incomplete", audits, err)
	}
	elapsed := time.Since(accepted)
	record["elapsed_seconds"], record["closed_accounts"], record["closed_journals"] = elapsed.Seconds(), closed, perGateway
	record["deleted_provider_clients"], record["deleted_provider_users"] = perGateway, perGateway
	record["target_met"] = elapsed <= 30*time.Second
	record["stage"] = "preservation"
	after := providerSnapshot()
	if !reflect.DeepEqual(before, after) {
		t.Fatal("cleanup changed a client outside the selected Gateway")
	}
	if capacityBackgroundSnapshot(t, ctx, f, selected) != beforeSQL {
		t.Fatal("cleanup changed a background account row")
	}
	record["preserved_background_accounts"], record["preserved_other_clients"] = len(background), len(after)
	record["background_sql_sha256"] = beforeSQL
	record["provider_snapshot_sha256"] = capacityMapDigest(after)
	record["complete"], record["stage"] = true, "complete"
	t.Logf("Confirmed %d account closures in %.3f seconds; %d background accounts stayed unchanged", perGateway, elapsed.Seconds(), len(background))
	if elapsed > 30*time.Second {
		t.Errorf("account cleanup missed the 30-second target: %.3f seconds", elapsed.Seconds())
	}
}

func capacityBackgroundSnapshot(t *testing.T, ctx context.Context, f *fixture, selected string) string {
	t.Helper()
	rows, err := f.db.QueryContext(ctx, "SELECT row_to_json(s)::text FROM service_accounts s WHERE gateway_id<>$1 ORDER BY id", selected)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	hash := sha256.New()
	count := 0
	for rows.Next() {
		var row string
		if err := rows.Scan(&row); err != nil {
			t.Fatal(err)
		}
		fmt.Fprintln(hash, row)
		count++
	}
	if err := rows.Err(); err != nil || count != 9900 {
		t.Fatal("background snapshot is incomplete", count, err)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func capacityMapDigest(values map[string]string) string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var data strings.Builder
	for _, key := range keys {
		fmt.Fprintf(&data, "%s:%s\n", key, values[key])
	}
	sum := sha256.Sum256([]byte(data.String()))
	return hex.EncodeToString(sum[:])
}
