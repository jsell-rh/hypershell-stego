package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	web "github.com/jsell-rh/hypershell-stego/out/application/client"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	"github.com/segmentio/ksuid"
)

// This checks real provider discovery, not a production capacity target.
func TestUnknownGatewayClientsCloseAcrossProcessRestart(t *testing.T) {
	k := startKeycloak(t)
	f := database(t)
	_, gateway := accountService(t, f, newAccountProvider())
	k.bindGateway(t, "gateway-audience", gateway.ID)
	oidc := fmt.Sprintf(`{"issuer":%q,"client_id":"gateway-audience","audience":"gateway-audience"}`, k.options.ServerURL+"/realms/workflow")
	if _, err := f.db.Exec("UPDATE gateways SET oidc=$1 WHERE id=$2", oidc, gateway.ID); err != nil {
		t.Fatal(err)
	}
	observeGatewayFixture(t, f, gateway.ID)
	key, baseSettings := issuer(t)
	providerSettings, stopProvider := startRealProvisioner(t, f, k, key, baseSettings)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	response, grant := k.issue(t, "provisioner", "acceptance-only-admin-secret")
	if response.StatusCode != http.StatusOK {
		t.Fatal("provider administrator login failed")
	}
	bearer, ok := grant["access_token"].(string)
	if !ok || bearer == "" {
		t.Fatal("provider administrator token is absent")
	}
	headers := http.Header{"Authorization": {"Bearer " + bearer}, "Content-Type": {"application/json"}}
	admin := func(method, path string, value any) web.Response {
		t.Helper()
		var body []byte
		var err error
		if value != nil {
			body, err = json.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
		}
		result, err := k.http.Do(ctx, method, "/admin/realms/workflow"+path, headers, body)
		if err != nil {
			t.Fatal("provider request failed", err)
		}
		return result
	}
	type clientIdentity struct{ Account, Provider, Name string }
	const count = 61
	const secret = "acceptance-only-unknown-client-secret"
	clients := make(map[string]clientIdentity, count)
	create := func(account string, attributes map[string]string) clientIdentity {
		t.Helper()
		name := "hs-sa-" + gateway.ID + "-" + account
		result := admin(http.MethodPost, "/clients", map[string]any{"clientId": name, "enabled": true, "protocol": "openid-connect", "secret": secret, "publicClient": false, "serviceAccountsEnabled": true, "standardFlowEnabled": false, "directAccessGrantsEnabled": false, "fullScopeAllowed": false, "attributes": attributes})
		if result.StatusCode != http.StatusCreated {
			t.Fatal("provider fixture creation failed", result.StatusCode)
		}
		location, err := url.Parse(result.Header.Get("Location"))
		if err != nil {
			t.Fatal("invalid provider location")
		}
		prefix := "/admin/realms/workflow/clients/"
		if !strings.HasPrefix(location.Path, prefix) {
			t.Fatal("unexpected provider location")
		}
		id := strings.TrimPrefix(location.Path, prefix)
		if id == "" || strings.Contains(id, "/") {
			t.Fatal("invalid provider identity")
		}
		return clientIdentity{Account: account, Provider: id, Name: name}
	}
	for i := range count {
		account := ksuid.New().String()
		prefix := ""
		if i%2 == 1 {
			prefix = "stego.owner."
		}
		attributes := map[string]string{prefix + "hypershell.service-account": "true", prefix + "hypershell.gateway-id": gateway.ID, prefix + "hypershell.service-account-id": account}
		clients[account] = create(account, attributes)
	}
	// A matching public name alone must not authorize deletion.
	foreign := create(ksuid.New().String(), map[string]string{"application.owner": "unrelated"})
	beforeForeign := admin(http.MethodGet, "/clients/"+foreign.Provider, nil)
	if beforeForeign.StatusCode != http.StatusOK {
		t.Fatal("unrelated client is absent")
	}
	scope := gateways.AccountProviderStateScope(gateway.ID)
	journalCount := func() int {
		t.Helper()
		var n int
		if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM stego_resource_state WHERE entity='ServiceAccount' AND scope=$1", scope).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	noAccountRows := func() {
		t.Helper()
		var n int
		if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM service_accounts WHERE gateway_id=$1", gateway.ID).Scan(&n); err != nil || n != 0 {
			t.Fatal("unknown clients gained account rows", n, err)
		}
	}
	noAccountRows()
	if journalCount() != 0 {
		t.Fatal("unknown clients already have cleanup journals")
	}
	settings := append(append([]string{}, baseSettings...), providerSettings...)
	stopAPI, address := startApplication(t, binary, f.dsn, config, settings...)
	owner := token(t, key, "alice")
	gatewayPath := "/api/hypershell/v1/gateways/" + gateway.ID
	if code, _ := requestJSON(t, http.MethodDelete, address+gatewayPath, owner, nil); code != http.StatusAccepted {
		t.Fatal("Gateway deletion was not accepted", code)
	}
	if code, _ := requestJSON(t, http.MethodPost, address+gatewayPath+"/service_accounts", owner, []byte(`{"name":"blocked"}`)); code != http.StatusNotFound {
		t.Fatal("deleting Gateway accepted an account request", code)
	}

	// Stop both processes only after a partial page set and its cursor are durable.
	deadline := time.Now().Add(time.Minute)
	var checkpointVersion int64
	for {
		checkpoint, err := f.storage.LoadCheckpoint(ctx, "Gateway", gateway.ID, "gateway-provider-inventory")
		if err != nil {
			t.Fatal(err)
		}
		n := journalCount()
		if n >= 20 && n < count && checkpoint.Version > 0 {
			checkpointVersion = checkpoint.Version
			break
		}
		if n >= count {
			t.Fatal("discovery completed before the process restart boundary")
		}
		if time.Now().After(deadline) {
			t.Fatal("real provider discovery did not retain partial progress")
		}
		time.Sleep(100 * time.Millisecond)
	}
	stopAPI()
	stopProvider()
	interruptedCount := journalCount()
	if interruptedCount == 0 || interruptedCount >= count {
		t.Fatal("process restart did not interrupt discovery", interruptedCount)
	}
	checkpoint, err := f.storage.LoadCheckpoint(ctx, "Gateway", gateway.ID, "gateway-provider-inventory")
	if err != nil || checkpoint.Version < checkpointVersion || checkpoint.After == "" {
		t.Fatal("discovery checkpoint was lost", err)
	}
	membership, err := f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scope)
	if err != nil || membership.Sealed {
		t.Fatal("incomplete discovery sealed its scope", err)
	}
	noAccountRows()
	providerSettings, _ = startRealProvisioner(t, f, k, key, baseSettings)
	settings = append(append([]string{}, baseSettings...), providerSettings...)
	_, address = startApplication(t, binary, f.dsn, config, settings...)
	for {
		membership, err = f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scope)
		if err != nil {
			t.Fatal(err)
		}
		if membership.Sealed {
			break
		}
		if ctx.Err() != nil {
			t.Fatal("unknown provider clients did not finish cleanup after restart")
		}
		time.Sleep(100 * time.Millisecond)
	}
	if journalCount() != count {
		t.Fatal("provider discovery did not retain every owned identity")
	}
	noAccountRows()
	for _, client := range clients {
		result := admin(http.MethodGet, "/clients/"+client.Provider, nil)
		if result.StatusCode != http.StatusNotFound {
			t.Fatal("an owned unknown client survived cleanup", result.StatusCode)
		}
	}
	afterForeign := admin(http.MethodGet, "/clients/"+foreign.Provider, nil)
	before, beforeOK := clientSnapshot(beforeForeign.Body)
	after, afterOK := clientSnapshot(afterForeign.Body)
	if afterForeign.StatusCode != http.StatusOK || !beforeOK || !afterOK || !reflect.DeepEqual(before, after) {
		// Report fixed field names and comparison results, never provider values.
		changed := []string{}
		for _, field := range []string{"id", "clientId", "name", "secret", "enabled", "attributes", "defaultClientScopes", "optionalClientScopes", "protocolMappers", "access", "redirectUris", "webOrigins"} {
			a, aPresent := before[field]
			b, bPresent := after[field]
			if aPresent != bPresent || !reflect.DeepEqual(a, b) {
				changed = append(changed, field)
			}
		}
		t.Fatalf("unrelated client check failed: HTTP=%d valid_snapshot=%t changed_known_fields=%v", afterForeign.StatusCode, beforeOK && afterOK, changed)
	}
	if !bytes.Equal(beforeForeign.Body, afterForeign.Body) {
		t.Log("Unrelated client response encoding changed; scope membership and all other JSON values stayed equal")
	}

	raw, err := os.ReadFile(k.stateKeysFile)
	if err != nil {
		t.Fatal(err)
	}
	protector, err := runtime.NewStateProtectorFromJSON(raw)
	clear(raw)
	if err != nil {
		t.Fatal(err)
	}
	rows, err := f.db.QueryContext(ctx, "SELECT resource_id,version,data FROM stego_resource_state WHERE entity='ServiceAccount' AND scope=$1", scope)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	checked := 0
	for rows.Next() {
		var id string
		var version int64
		var sealed []byte
		if err := rows.Scan(&id, &version, &sealed); err != nil {
			t.Fatal(err)
		}
		client, exists := clients[id]
		if !exists || bytes.Contains(sealed, []byte(secret)) {
			t.Fatal("unexpected journal identity or exposed credential")
		}
		state, err := protector.Open(runtime.StateKey{Instance: k.instanceID, Entity: "ServiceAccount", ResourceID: id, Scope: scope}, version, sealed)
		if err != nil {
			t.Fatal("unknown client journal did not authenticate", err)
		}
		plain := state.Reveal()
		var record struct {
			Closed  bool
			Binding struct{ ID, ClientID string }
		}
		decodeErr := json.Unmarshal(plain, &record)
		exposed := bytes.Contains(plain, []byte(secret))
		clear(plain)
		if decodeErr != nil || exposed || !record.Closed || record.Binding.ID != client.Provider || record.Binding.ClientID != client.Name {
			t.Fatal("unknown client closure lost its protected identity")
		}
		checked++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if checked != count {
		t.Fatal("incomplete protected closure check", checked)
	}
	if code, _ := requestJSON(t, http.MethodPost, address+gatewayPath+"/service_accounts", owner, []byte(`{"name":"still-blocked"}`)); code != http.StatusNotFound {
		t.Fatal("restart reopened account creation", code)
	}
	t.Logf("Unknown provider cleanup retained %d closed protected identities; %d journals and a checkpoint survived API/provisioner restart; the matching-name unrelated client was unchanged", checked, interruptedCount)
}

// Keycloak returns scope names from a map. Compare those two fields as sets.
// All other fields, including unknown fields and array order, stay significant.
func clientSnapshot(raw []byte) (map[string]any, bool) {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	var value map[string]any
	if d.Decode(&value) != nil || value == nil || d.Decode(new(any)) != io.EOF {
		return nil, false
	}
	for _, field := range []string{"defaultClientScopes", "optionalClientScopes"} {
		items, ok := value[field].([]any)
		if !ok {
			return nil, false
		}
		names := make([]string, len(items))
		for i, item := range items {
			name, ok := item.(string)
			if !ok || name == "" {
				return nil, false
			}
			names[i] = name
		}
		slices.Sort(names)
		for i := 1; i < len(names); i++ {
			if names[i] == names[i-1] {
				return nil, false
			}
		}
		value[field] = names
	}
	return value, true
}

func TestUnrelatedClientSnapshotComparison(t *testing.T) {
	const original = `{"defaultClientScopes":["profile","email"],"optionalClientScopes":[],"enabled":true,"secret":"fixture","attributes":{"owner":"foreign"},"other":[1,2],"counter":9007199254740993}`
	before, ok := clientSnapshot([]byte(original))
	if !ok {
		t.Fatal("invalid comparison fixture")
	}
	for _, tc := range []struct {
		name, from, to string
		wantEqual      bool
	}{
		{"same", "", "", true},
		{"scope_order", `["profile","email"]`, `["email","profile"]`, true},
		{"scope_added", `["profile","email"]`, `["profile","email","roles"]`, false},
		{"scope_removed", `["profile","email"]`, `["profile"]`, false},
		{"scope_duplicate", `["profile","email"]`, `["profile","email","email"]`, false},
		{"scope_type", `["profile","email"]`, `["profile",1]`, false},
		{"scope_null", `["profile","email"]`, `null`, false},
		{"scope_missing", `"defaultClientScopes":["profile","email"],`, ``, false},
		{"optional_added", `"optionalClientScopes":[]`, `"optionalClientScopes":["roles"]`, false},
		{"disabled", `true`, `false`, false},
		{"credential_changed", `"fixture"`, `"changed"`, false},
		{"owner_changed", `"foreign"`, `"managed"`, false},
		{"other_order", `[1,2]`, `[2,1]`, false},
		{"large_number", `9007199254740993`, `9007199254740992`, false},
		{"trailing_value", `9007199254740993}`, `9007199254740993}{}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw := original
			if tc.from != "" {
				raw = strings.Replace(raw, tc.from, tc.to, 1)
			}
			after, valid := clientSnapshot([]byte(raw))
			if got := valid && reflect.DeepEqual(before, after); got != tc.wantEqual {
				t.Fatalf("snapshot equality = %t, want %t", got, tc.wantEqual)
			}
		})
	}
}
