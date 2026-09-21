package acceptance

import (
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"testing"

	"github.com/segmentio/ksuid"
)

func TestRESTRoleMappingRejectsStoredFaultsAcrossRestart(t *testing.T) {
	f := database(t)
	valid, faulty := ksuid.New().String(), ksuid.New().String()
	for id, name := range map[string]string{valid: "a-compatible-role", faulty: "z-invalid-role"} {
		if _, err := f.db.Exec(`INSERT INTO roles(id,name,permissions,built_in,created_time,updated_time)
 VALUES($1,$2,'{"gateways":["read"]}',false,now(),now())`, id, name); err != nil {
			t.Fatal(err)
		}
	}
	key, settings := issuer(t)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	bearer := token(t, key, "role-reader")
	base := func() string { return address + "/api/hypershell/v1/roles" }
	wantError := map[string]any{
		"id": "9", "kind": "Error", "href": "/api/hypershell/v1/errors/9",
		"code": "hypershell-9", "reason": "An internal error occurred", "operation_id": "",
	}
	for _, fault := range []struct{ name, damage, repair string }{
		{"scalar", "UPDATE roles SET permissions=jsonb_build_object('key',repeat('private-role-value',1000)) WHERE id=$1", "UPDATE roles SET permissions='{}' WHERE id=$1"},
		{"nodes", "UPDATE roles SET permissions=jsonb_build_object('list',(SELECT jsonb_agg(0) FROM generate_series(1,4094))) WHERE id=$1", "UPDATE roles SET permissions='{}' WHERE id=$1"},
		{"timestamp", "UPDATE roles SET updated_time=make_timestamptz(10000,1,1,0,0,0,'UTC') WHERE id=$1", "UPDATE roles SET updated_time=now() WHERE id=$1"},
	} {
		update := func(statement string) {
			t.Helper()
			result, err := f.db.Exec(statement, faulty)
			if err != nil {
				t.Fatal(err)
			}
			if count, err := result.RowsAffected(); err != nil || count != 1 {
				t.Fatal("fault did not select one Role", err)
			}
		}
		update(fault.damage)
		for phase := 0; phase < 2; phase++ {
			if phase == 1 {
				stop()
				stop, address = startApplication(t, binary, f.dsn, config, settings...)
			}
			t.Run(fmt.Sprintf("%s/restart-%d", fault.name, phase), func(t *testing.T) {
				for _, suffix := range []string{"/" + faulty, "?size=100&orderBy=name", "?size=100&fields=id,name"} {
					code, body := requestJSON(t, "GET", base()+suffix, bearer, nil)
					var got map[string]any
					if code != 500 || json.Unmarshal(body, &got) != nil || !reflect.DeepEqual(got, wantError) {
						t.Fatal("invalid Role returned partial data or changed its private error", code)
					}
					if code, _ := requestJSON(t, "GET", base()+suffix, "", nil); code != 401 {
						t.Fatal("Role mapping ran before authentication", code)
					}
				}
				for _, query := range []string{"?search=" + url.QueryEscape("name = 'a-compatible-role'"), "?size=1&orderBy=name"} {
					code, body := requestJSON(t, "GET", base()+query, bearer, nil)
					var page roleListResponse
					if code != 200 || json.Unmarshal(body, &page) != nil || page.Size != 1 || len(page.Items) != 1 || page.Items[0].ID != valid {
						t.Fatal("Role mapping ran before selection", code)
					}
				}
				code, body := requestJSON(t, "GET", base()+"/"+valid, bearer, nil)
				var role roleResponse
				if code != 200 || json.Unmarshal(body, &role) != nil || role.ID != valid || role.BuiltIn {
					t.Fatal("valid Role retrieval changed", code)
				}
				code, body = requestJSON(t, "GET", base()+"?size=0", bearer, nil)
				var count roleListResponse
				if code != 200 || json.Unmarshal(body, &count) != nil || count.Total != 6 || count.Size != 0 || count.Items == nil || len(count.Items) != 0 {
					t.Fatal("count-only Role response changed", code)
				}
			})
		}
		update(fault.repair)
		code, body := requestJSON(t, "GET", base()+"?size=100", bearer, nil)
		var page roleListResponse
		if code != 200 || json.Unmarshal(body, &page) != nil || page.Total != 6 || page.Size != 6 || len(page.Items) != 6 {
			t.Fatal("repair did not restore the complete Role list", code)
		}
	}
}
