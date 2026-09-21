package acceptance

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"testing"
)

func TestRESTGatewayGrantMappingsRejectStoredFaults(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	owner := token(t, key, "mapping-owner", "gateway:creator")
	outsider := token(t, key, "mapping-outsider")
	type entry struct {
		path, good, bad string
		total           int
	}
	gateways := entry{path: "gateways"}
	grants := entry{path: "role_bindings"}
	for _, name := range []string{"a-valid", "z-invalid"} {
		data, e := json.Marshal(f.request(name))
		if e != nil {
			t.Fatal(e)
		}
		code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, data)
		var row gatewayResponse
		if code != 201 || json.Unmarshal(body, &row) != nil || row.ID == "" {
			t.Fatal("cannot create conversion fixture", code)
		}
		var grantID string
		if e := f.db.QueryRow("SELECT id FROM role_bindings WHERE gateway_id=$1 AND deleted_at IS NULL", row.ID).Scan(&grantID); e != nil {
			t.Fatal(e)
		}
		if name == "a-valid" {
			gateways.good, grants.good = row.ID, grantID
		} else {
			gateways.bad, grants.bad = row.ID, grantID
		}
	}
	entries := []entry{gateways, grants}
	for i, item := range entries {
		code, body := requestJSON(t, "GET", address+"/api/hypershell/v1/"+item.path+"?size=0", owner, nil)
		var count struct {
			Total int `json:"total"`
		}
		if code != 200 || json.Unmarshal(body, &count) != nil || count.Total < 2 {
			t.Fatal("cannot read fixture count", code)
		}
		entries[i].total = count.Total
	}
	// These fixed tables belong to this fixture database. No other database is changed.
	faults := []struct{ sql, id string }{{"UPDATE gateways SET server_dns_names='[null]'::jsonb WHERE id=$1", gateways.bad}, {"UPDATE role_bindings SET updated_time=make_timestamptz(10000,1,1,0,0,0,'UTC') WHERE id=$1", grants.bad}}
	for _, fault := range faults {
		result, e := f.db.Exec(fault.sql, fault.id)
		if e != nil {
			t.Fatal(e)
		}
		if count, e := result.RowsAffected(); e != nil || count != 1 {
			t.Fatal("fault did not select one row", e)
		}
	}
	wantError := map[string]any{"id": "9", "kind": "Error", "href": "/api/hypershell/v1/errors/9", "code": "hypershell-9", "reason": "An internal error occurred", "operation_id": ""}
	for phase := 0; phase < 2; phase++ {
		if phase == 1 {
			stop()
			stop, address = startApplication(t, binary, f.dsn, config, settings...)
		}
		for _, item := range entries {
			t.Run(fmt.Sprintf("%s/restart-%d", item.path, phase), func(t *testing.T) {
				base := address + "/api/hypershell/v1/" + item.path
				for _, suffix := range []string{"/" + item.bad, "?size=100", "?size=100&fields=id", "?search=" + url.QueryEscape("id = '"+item.bad+"'")} {
					code, body := requestJSON(t, "GET", base+suffix, owner, nil)
					var got map[string]any
					if code != 500 || json.Unmarshal(body, &got) != nil || !reflect.DeepEqual(got, wantError) {
						t.Fatal("invalid row returned partial data or changed the public error", code, string(body))
					}
				}
				for _, denied := range []struct {
					bearer string
					code   int
				}{{outsider, 404}, {"", 401}} {
					if code, _ := requestJSON(t, "GET", base+"/"+item.bad, denied.bearer, nil); code != denied.code {
						t.Fatal("conversion changed denied access", code)
					}
				}
				code, body := requestJSON(t, "GET", base+"?size=100", outsider, nil)
				var hidden struct {
					Items []json.RawMessage `json:"items"`
					Total int               `json:"total"`
				}
				if code != 200 || json.Unmarshal(body, &hidden) != nil || hidden.Total != 0 || len(hidden.Items) != 0 {
					t.Fatal("conversion ran before access filtering", code)
				}
				code, body = requestJSON(t, "GET", base+"?search="+url.QueryEscape("id = '"+item.good+"'"), owner, nil)
				var selected struct {
					Items []struct {
						ID string `json:"id"`
					} `json:"items"`
				}
				if code != 200 || json.Unmarshal(body, &selected) != nil || len(selected.Items) != 1 || selected.Items[0].ID != item.good {
					t.Fatal("valid selection did not exclude the faulty row", code)
				}
				code, body = requestJSON(t, "GET", base+"?size=0", owner, nil)
				var count struct {
					Total int             `json:"total"`
					Items json.RawMessage `json:"items"`
				}
				if code != 200 || json.Unmarshal(body, &count) != nil || count.Total != item.total || string(count.Items) != "[]" {
					t.Fatal("count-only response changed", code)
				}
			})
		}
	}
	for _, repair := range []struct{ sql, id string }{{"UPDATE gateways SET server_dns_names='[]'::jsonb WHERE id=$1", gateways.bad}, {"UPDATE role_bindings SET updated_time=created_time WHERE id=$1", grants.bad}} {
		if _, e := f.db.Exec(repair.sql, repair.id); e != nil {
			t.Fatal(e)
		}
	}
	for _, item := range entries {
		code, body := requestJSON(t, "GET", address+"/api/hypershell/v1/"+item.path+"?size=100", owner, nil)
		var page struct {
			Items []json.RawMessage `json:"items"`
			Total int               `json:"total"`
		}
		if code != 200 || json.Unmarshal(body, &page) != nil || page.Total != item.total || len(page.Items) != item.total {
			t.Fatal("response did not recover after repair", code)
		}
	}
}
