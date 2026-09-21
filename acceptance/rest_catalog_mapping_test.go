package acceptance

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"testing"
)

func TestRESTCatalogMappingRejectsStoredTimestamp(t *testing.T) {
	f := databaseSetup(t, false)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	admin := token(t, key, "operator", "platform:admin")
	creator := token(t, key, "reader", "gateway:creator")
	outsider := token(t, key, "outsider")
	type entry struct{ path, good, bad string }
	var entries []entry
	for _, item := range []struct {
		path   string
		fields map[string]any
	}{
		{"managed_clusters", map[string]any{"provider": "kubernetes", "kubeconfig_secret": "cluster-access"}},
		{"gateway_releases", map[string]any{"image": "registry.example/gateway:v1"}},
		{"gateway_networks", map[string]any{"topology": "mesh"}},
	} {
		row := entry{path: item.path}
		for _, name := range []string{"a-valid", "z-invalid"} {
			item.fields["name"] = name
			body, err := json.Marshal(item.fields)
			if err != nil {
				t.Fatal(err)
			}
			code, data := requestJSON(t, "POST", address+"/api/hypershell/v1/"+item.path, admin, body)
			var response struct {
				ID string `json:"id"`
			}
			if code != 201 || json.Unmarshal(data, &response) != nil || response.ID == "" {
				t.Fatal("cannot create catalog conversion fixture", code)
			}
			if name == "a-valid" {
				row.good = response.ID
			} else {
				row.bad = response.ID
			}
		}
		// The table name is fixed by this test. Only this fixture database is used.
		result, err := f.db.Exec("UPDATE "+item.path+" SET updated_time=make_timestamptz(10000,1,1,0,0,0,'UTC') WHERE id=$1", row.bad)
		if err != nil {
			t.Fatal(err)
		}
		if count, err := result.RowsAffected(); err != nil || count != 1 {
			t.Fatal("timestamp fault did not select one fixture row", err)
		}
		entries = append(entries, row)
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
				order := "orderBy=" + url.QueryEscape("name asc")
				for _, suffix := range []string{"/" + item.bad, "?" + order, "?" + order + "&fields=id,name", "?" + order + "&size=1&page=2"} {
					code, body := requestJSON(t, "GET", base+suffix, creator, nil)
					var got map[string]any
					if code != 500 || json.Unmarshal(body, &got) != nil || !reflect.DeepEqual(got, wantError) {
						t.Fatal("invalid stored timestamp returned partial data or changed the public error", code, string(body))
					}
				}
				for _, denied := range []struct {
					bearer string
					status int
				}{{outsider, 403}, {"", 401}} {
					if code, _ := requestJSON(t, "GET", base+"?"+order, denied.bearer, nil); code != denied.status {
						t.Fatal("conversion changed access denial", code)
					}
				}
				for _, suffix := range []string{"?search=" + url.QueryEscape("id = '"+item.good+"'"), "?" + order + "&size=1&page=1"} {
					code, body := requestJSON(t, "GET", base+suffix, creator, nil)
					var page struct {
						Items []struct {
							ID string `json:"id"`
						} `json:"items"`
					}
					if code != 200 || json.Unmarshal(body, &page) != nil || len(page.Items) != 1 || page.Items[0].ID != item.good {
						t.Fatal("selection did not run before response conversion", code)
					}
				}
				code, body := requestJSON(t, "GET", base+"?size=0", creator, nil)
				var count struct {
					Total int             `json:"total"`
					Items json.RawMessage `json:"items"`
				}
				if code != 200 || json.Unmarshal(body, &count) != nil || count.Total != 2 || string(count.Items) != "[]" {
					t.Fatal("count-only response changed", code)
				}
			})
		}
	}
	for _, item := range entries {
		if _, err := f.db.Exec("UPDATE "+item.path+" SET updated_time=created_time WHERE id=$1", item.bad); err != nil {
			t.Fatal(err)
		}
		code, body := requestJSON(t, "GET", address+"/api/hypershell/v1/"+item.path, creator, nil)
		var page struct {
			Items []json.RawMessage `json:"items"`
			Total int               `json:"total"`
		}
		if code != 200 || json.Unmarshal(body, &page) != nil || len(page.Items) != 2 || page.Total != 2 {
			t.Fatal("catalog did not recover after the stored timestamp was repaired", code)
		}
	}
}
