package acceptance

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
)

func TestRESTSearchAndOrderingPreserveGatewayAccess(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	stop, address := startApplication(t, buildApplication(t), f.dsn, config, settings...)
	defer stop()
	path := address + "/api/hypershell/v1/gateways"
	for _, item := range []struct{ name, user string }{{"a-allowed", "alice"}, {"z-allowed", "alice"}, {"b-hidden", "bob"}} {
		body := []byte(fmt.Sprintf(`{"name":%q,"cluster_id":%q,"release_id":%q,"database_id":"ignored"}`, item.name, f.cluster, f.release))
		code, data := requestJSON(t, "POST", path, token(t, key, item.user, "gateway:creator"), body)
		if code != 201 {
			t.Fatalf("create: %d %s", code, data)
		}
	}
	owner := token(t, key, "alice")
	for _, test := range []struct {
		search, order string
		size, total   int
		first         string
	}{
		{"name like '%allowed'", "name desc", 1, 2, "z-allowed"},
		{"name = 'b-hidden' or name = 'a-allowed' or name = 'z-allowed'", "name asc", 20, 2, "a-allowed"},
		{"name = 'b-hidden'", "", 20, 0, ""},
		{"name = 'a-allowed'", "", 0, 1, ""},
		{"name = 'x'' OR TRUE --'", "", 20, 0, ""},
		{"name ilike 'A-%' and created_at > 2020-01-01", "created_at DESC", 20, 1, "a-allowed"},
	} {
		query := url.Values{"search": {test.search}, "orderBy": {test.order}, "size": {fmt.Sprint(test.size)}}
		code, data := requestJSON(t, "GET", path+"?"+query.Encode(), owner, nil)
		var result httpapi.GatewayList
		if code != 200 || json.Unmarshal(data, &result) != nil || result.Total != int64(test.total) {
			t.Fatalf("search %s: %d %s", test.search, code, data)
		}
		if test.first != "" && (len(result.Items) == 0 || result.Items[0].Name != test.first) {
			t.Fatalf("ordering: %s", data)
		}
		if test.size == 0 && len(result.Items) != 0 {
			t.Fatal("count-only fetched Gateway rows")
		}
		for _, item := range result.Items {
			if item.Name == "b-hidden" {
				t.Fatal("search bypassed access")
			}
		}
	}
	for _, query := range []url.Values{
		{"search": {`"name) OR TRUE --" = 'x'`}},
		{"search": {"name = 'a' or unknown = 'b'"}},
		{"search": {"active_sandbox_count like 'x'"}},
		{"search": {"created_at = 'not-a-date'"}},
		{"search": {"name = false"}},
		{"search": {"active_sandbox_count = 'not-an-integer'"}},
		{"search": {"name='a';SELECT 1"}},
		{"search": {"name = 'a'", "name = 'b'"}},
		{"search": {strings.Repeat("(", 33) + "name='a'" + strings.Repeat(")", 33)}},
		{"orderBy": {"name DESC; SELECT 1"}},
		{"orderBy": {"unknown"}, "size": {"0"}},
	} {
		code, data := requestJSON(t, "GET", path+"?"+query.Encode(), owner, nil)
		if code != 400 || strings.Contains(string(data), "SQLSTATE") {
			t.Fatalf("invalid query: %d %s", code, data)
		}
	}
}
