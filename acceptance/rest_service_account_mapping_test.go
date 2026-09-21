package acceptance

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"strings"
	"testing"
)

func TestRESTAccountMappingRejectsStoredFaultsAcrossRestart(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	_, gateway := accountService(t, f, provider)
	grantViewer(t, f, gateway.ID, "bob")
	key, settings := issuer(t)
	rpcSettings, _ := startAccountProvisioner(t, provider, key, settings)
	settings = append(settings, rpcSettings...)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	owner, viewer, outsider := token(t, key, "alice"), token(t, key, "bob"), token(t, key, "mallory")
	base := func() string { return address + "/api/hypershell/v1/gateways/" + gateway.ID + "/service_accounts" }
	ids := map[string]string{}
	for _, input := range []struct{ name, bearer string }{{"a-valid", owner}, {"z-invalid", owner}, {"viewer-account", viewer}} {
		body, err := json.Marshal(map[string]string{"name": input.name})
		if err != nil {
			t.Fatal(err)
		}
		code, body := requestJSON(t, "POST", base(), input.bearer, body)
		var row map[string]any
		if code != 201 || json.Unmarshal(body, &row) != nil {
			t.Fatal("cannot create account fixture", code)
		}
		id, ok := row["id"].(string)
		if !ok || id == "" {
			t.Fatal("account ID missing")
		}
		ids[input.name] = id
		for _, field := range []string{"description", "revoked_at", "last_error"} {
			value, present := row[field]
			if !present || value != nil {
				t.Fatal("create changed explicit null", field)
			}
		}
		credential, ok := row["credential"].(map[string]any)
		if !ok || credential["client_secret"] == "" || credential["client_secret"] == nil {
			t.Fatal("one-time credential missing")
		}
	}
	faulty := ids["z-invalid"]
	faults := []struct{ name, damage, repair string }{
		{"nullable_timestamp", "UPDATE service_accounts SET revoked_at=make_timestamptz(10000,1,1,0,0,0,'UTC') WHERE id=$1", "UPDATE service_accounts SET revoked_at=NULL WHERE id=$1"},
		{"enum", "UPDATE service_accounts SET credential_type='private-unknown-credential' WHERE id=$1", "UPDATE service_accounts SET credential_type='client_secret' WHERE id=$1"},
	}
	update := func(statement string) {
		t.Helper()
		result, err := f.db.Exec(statement, faulty)
		if err != nil {
			t.Fatal(err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Fatal("fault did not select one account", err)
		}
	}
	wantError := map[string]any{"code": "internal_error", "reason": "The request could not be completed"}
	for _, fault := range faults {
		update(fault.damage)
		for phase := 0; phase < 2; phase++ {
			if phase == 1 {
				stop()
				stop, address = startApplication(t, binary, f.dsn, config, settings...)
			}
			t.Run(fmt.Sprintf("%s/restart-%d", fault.name, phase), func(t *testing.T) {
				for _, suffix := range []string{"/" + faulty, "?size=100&sort=name&order=asc", "?search=" + url.QueryEscape("z-invalid")} {
					code, body := requestJSON(t, "GET", base()+suffix, owner, nil)
					var got map[string]any
					if code != 500 || json.Unmarshal(body, &got) != nil || !reflect.DeepEqual(got, wantError) {
						t.Fatal("invalid account returned partial data or changed its private error", code)
					}
				}
				for _, denied := range []struct {
					bearer string
					code   int
				}{{viewer, 404}, {outsider, 404}, {"", 401}} {
					if code, _ := requestJSON(t, "GET", base()+"/"+faulty, denied.bearer, nil); code != denied.code {
						t.Fatal("mapping changed denied access", code)
					}
				}
				for _, selection := range []struct{ bearer, query, id string }{{viewer, "?size=100", ids["viewer-account"]}, {owner, "?search=a-valid", ids["a-valid"]}} {
					code, body := requestJSON(t, "GET", base()+selection.query, selection.bearer, nil)
					var page struct {
						Total int `json:"total"`
						Items []struct {
							ID string `json:"id"`
						} `json:"items"`
					}
					if code != 200 || json.Unmarshal(body, &page) != nil || page.Total != 1 || len(page.Items) != 1 || page.Items[0].ID != selection.id {
						t.Fatal("mapping ran before filtering", code)
					}
					if strings.Contains(string(body), "one-time-private-") || strings.Contains(string(body), "\"client_secret\":") {
						t.Fatal("list disclosed credentials")
					}
				}
				code, body := requestJSON(t, "GET", base()+"/"+ids["a-valid"], owner, nil)
				var row map[string]any
				if code != 200 || json.Unmarshal(body, &row) != nil || row["connection"] == nil || row["id"] != ids["a-valid"] {
					t.Fatal("valid account retrieval changed", code)
				}
				if _, present := row["credential"]; present {
					t.Fatal("get disclosed one-time credentials")
				}
			})
		}
		update(fault.repair)
		code, body := requestJSON(t, "GET", base()+"?size=100", owner, nil)
		var page struct {
			Total int               `json:"total"`
			Items []json.RawMessage `json:"items"`
		}
		if code != 200 || json.Unmarshal(body, &page) != nil || page.Total != 3 || len(page.Items) != 3 {
			t.Fatal("repair did not restore the complete account list", code)
		}
	}
	code, body := requestJSON(t, "POST", base()+"/"+faulty+"/revoke", owner, nil)
	var revoked map[string]any
	if code != 200 || json.Unmarshal(body, &revoked) != nil || revoked["id"] != faulty || revoked["status"] != "revoked" || revoked["revoked_at"] == nil {
		t.Fatal("revoke response changed", code)
	}
	for _, field := range []string{"description", "last_error"} {
		value, present := revoked[field]
		if !present || value != nil {
			t.Fatal("revoke changed explicit null", field)
		}
	}
	if code, body := requestJSON(t, "DELETE", base()+"/"+faulty, owner, nil); code != 204 || len(body) != 0 {
		t.Fatal("delete response changed", code)
	}
}
