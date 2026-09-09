package acceptance

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/contracts"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"github.com/segmentio/ksuid"
)

func applyRoleCatalog(ctx context.Context, db *sql.DB) error {
	data, err := os.ReadFile("../migrations/000004_role_catalog.sql")
	if err != nil {
		return err
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	if _, err = conn.ExecContext(ctx, string(data)); err != nil {
		cleanup, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_, _ = conn.ExecContext(cleanup, "ROLLBACK")
	}
	return err
}
func discoverRole(t testing.TB, root, bearer, name string) httpapi.Role {
	t.Helper()
	code, body := requestJSON(t, "GET", root+"/roles?search="+url.QueryEscape("name = '"+name+"'"), bearer, nil)
	var result httpapi.RoleList
	if code != 200 || json.Unmarshal(body, &result) != nil || result.Total != 1 || len(result.Items) != 1 || result.Items[0].Name != name {
		t.Fatal("role discovery", code, string(body))
	}
	return result.Items[0]
}

func TestRoleCatalogMigrationPreservesIDsAndGrants(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	legacy := ksuid.New().String()
	if _, err := f.db.Exec("UPDATE roles SET id=$1 WHERE name='gateway:owner'", legacy); err != nil {
		t.Fatal(err)
	}
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("legacy-owner"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`DELETE FROM roles WHERE name IN ('platform:admin','gateway:creator');
 ALTER TABLE roles DROP COLUMN display_name,DROP COLUMN description,DROP COLUMN permissions,DROP COLUMN built_in`); err != nil {
		t.Fatal(err)
	}
	custom := ksuid.New().String()
	if _, err := f.db.Exec("INSERT INTO roles(id,name,created_time,updated_time) VALUES($1,'custom-role',now(),now())", custom); err != nil {
		t.Fatal(err)
	}
	if err := applyRoleCatalog(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	var id string
	var builtIn bool
	var updated time.Time
	if err := f.db.QueryRow("SELECT id,built_in,updated_time FROM roles WHERE name='gateway:owner'").Scan(&id, &builtIn, &updated); err != nil || id != legacy || !builtIn {
		t.Fatal("owner identity changed", id, builtIn, err)
	}
	if _, err := f.service.Get(ctx, principal("alice"), gateway.ID); err != nil {
		t.Fatal("migration broke grant", err)
	}
	if err := applyRoleCatalog(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	var after time.Time
	if err := f.db.QueryRow("SELECT updated_time FROM roles WHERE id=$1", legacy).Scan(&after); err != nil || !after.Equal(updated) {
		t.Fatal("repeat migration changed current metadata", err)
	}
	if err := f.db.QueryRow("SELECT built_in FROM roles WHERE id=$1", custom).Scan(&builtIn); err != nil || builtIn {
		t.Fatal("custom role changed", err)
	}
	if _, err := f.db.Exec("UPDATE roles SET permissions='[]' WHERE id=$1", legacy); err == nil {
		t.Fatal("non-object permissions accepted")
	}
	if _, err := f.db.Exec("UPDATE roles SET display_name='changed' WHERE id=$1", legacy); err != nil {
		t.Fatal(err)
	}
	if err := applyRoleCatalog(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	var display string
	if err := f.db.QueryRow("SELECT display_name FROM roles WHERE id=$1", legacy).Scan(&display); err != nil || display != "Gateway Owner" {
		t.Fatal("seed repair", display, err)
	}
}

func TestRoleCatalogMigrationDoesNotRestoreDeletedRoles(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	if _, err := f.db.Exec("UPDATE roles SET deleted_at=now() WHERE name='gateway:viewer'; UPDATE roles SET display_name='unchanged' WHERE name='gateway:owner'"); err != nil {
		t.Fatal(err)
	}
	if err := applyRoleCatalog(ctx, f.db); err == nil {
		t.Fatal("deleted built-in role restored")
	}
	var deleted bool
	var display string
	if err := f.db.QueryRow("SELECT deleted_at IS NOT NULL FROM roles WHERE name='gateway:viewer'").Scan(&deleted); err != nil || !deleted {
		t.Fatal("role restored", err)
	}
	if err := f.db.QueryRow("SELECT display_name FROM roles WHERE name='gateway:owner'").Scan(&display); err != nil || display != "unchanged" {
		t.Fatal("failed seed changed metadata", err)
	}
}

func TestRoleDiscoveryThroughGeneratedRuntime(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	key, settings := issuer(t)
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	root := address + "/api/hypershell/v1"
	bearer := token(t, key, "new-user")
	reference, err := contracts.Load(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	listSchema := reference.OpenAPI.Paths.Value("/api/hypershell/v1/roles").Get.Responses.Status(200).Value.Content.Get("application/json").Schema.Value
	list := func(query string, total int64) httpapi.RoleList {
		t.Helper()
		code, body := requestJSON(t, "GET", root+"/roles"+query, bearer, nil)
		var value any
		var result httpapi.RoleList
		if code != 200 || json.Unmarshal(body, &value) != nil || listSchema.VisitJSON(value) != nil || json.Unmarshal(body, &result) != nil || result.Total != total || result.Size != len(result.Items) {
			t.Fatal("role list", code, string(body))
		}
		return result
	}
	got := list("?orderBy=name%20asc", 4)
	expected := map[string]map[string][]string{
		"platform:admin":  {"gateways": {"read", "delete"}},
		"gateway:creator": {"gateways": {"create"}, "role_bindings": {"create", "read", "delete", "list"}},
		"gateway:owner":   {"gateways": {"read", "update", "delete"}, "role_bindings": {"create", "read", "delete", "list"}},
		"gateway:viewer":  {"gateways": {"read"}},
	}
	ids := map[string]string{}
	for _, role := range got.Items {
		var permissions map[string][]string
		want, _ := json.Marshal(expected[role.Name])
		have := map[string][]string{}
		if err := json.Unmarshal(want, &have); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(role.Permissions, &permissions); err != nil {
			t.Fatal(err)
		}
		actual, _ := json.Marshal(permissions)
		canonical, _ := json.Marshal(have)
		if string(actual) != string(canonical) || !role.BuiltIn || role.DisplayName == nil || role.Description == nil || role.CreatedAt.IsZero() || role.UpdatedAt.IsZero() {
			t.Fatal("built-in metadata", role)
		}
		if _, err := ksuid.Parse(role.ID); err != nil {
			t.Fatal(err)
		}
		if role.Kind != "Role" || role.Href != "/api/hypershell/v1/roles/"+role.ID {
			t.Fatal("role reference", role)
		}
		ids[role.Name] = role.ID
		code, body := requestJSON(t, "GET", root+"/roles/"+role.ID, bearer, nil)
		var single httpapi.Role
		if code != 200 || json.Unmarshal(body, &single) != nil || single.ID != role.ID || single.Name != role.Name {
			t.Fatal("get role", code, string(body))
		}
	}
	first := list("?size=2&orderBy=name", 4)
	second := list("?size=2&page=2&orderBy=name", 4)
	if len(first.Items) != 2 || len(second.Items) != 2 || first.Items[0].ID == second.Items[0].ID {
		t.Fatal("role paging")
	}
	if len(list("?size=0", 4).Items) != 0 {
		t.Fatal("count-only returned roles")
	}
	if discoverRole(t, root, bearer, "gateway:viewer").ID != ids["gateway:viewer"] {
		t.Fatal("role ID changed")
	}
	list("?search="+url.QueryEscape("built_in = true"), 4)
	list("?search="+url.QueryEscape("name = 'missing'"), 0)
	for _, query := range []string{"?size=101", "?size=-1", "?page=0", "?size=1&size=2", "?search=" + url.QueryEscape("unknown = 'x'"), "?orderBy=unknown", "?fields=id"} {
		if code, _ := requestJSON(t, "GET", root+"/roles"+query, bearer, nil); code != 400 {
			t.Fatal("invalid role query", query, code)
		}
	}
	for _, path := range []string{"/roles", "/roles/" + ids["gateway:owner"]} {
		for _, token := range []string{"", "forged"} {
			if code, _ := requestJSON(t, "GET", root+path, token, nil); code != 401 {
				t.Fatal("role authentication", code)
			}
		}
	}
	for _, id := range []string{"bad-id", ksuid.New().String()} {
		if code, _ := requestJSON(t, "GET", root+"/roles/"+id, bearer, nil); code != 404 {
			t.Fatal("missing role", code)
		}
	}
	rejectMethod := func(method, path string) int {
		t.Helper()
		request, err := http.NewRequest(method, root+path, bytes.NewBufferString(`{"built_in":false}`))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+bearer)
		response, err := (&http.Client{Timeout: 5 * time.Second}).Do(request)
		if err != nil {
			t.Fatal(err)
		}
		response.Body.Close()
		return response.StatusCode
	}
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		if code := rejectMethod(method, "/roles/"+ids["gateway:owner"]); code != 405 {
			t.Fatal("role mutation route", method, code)
		}
	}
	if code := rejectMethod("POST", "/roles"); code != 405 {
		t.Fatal("role creation route", code)
	}
	// Discovery does not grant Gateway creation rights.
	request, _ := json.Marshal(f.request("denied"))
	if code, _ := requestJSON(t, "POST", root+"/gateways", bearer, request); code != 403 {
		t.Fatal("catalog read granted authority", code)
	}

	owner := token(t, key, "alice", "gateway:creator")
	request, _ = json.Marshal(f.request("catalog-grants"))
	code, body := requestJSON(t, "POST", root+"/gateways", owner, request)
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &gateway) != nil {
		t.Fatal("create from catalog workflow", code, string(body))
	}
	code, body = requestJSON(t, "GET", root+"/role_bindings?search="+url.QueryEscape("scope = 'gateway'"), owner, nil)
	var grants grantListResponse
	if code != 200 || json.Unmarshal(body, &grants) != nil || len(grants.Items) != 1 {
		t.Fatal("discover owner grant", code, string(body))
	}
	for _, name := range []string{"platform:admin", "gateway:creator"} {
		payload, _ := json.Marshal(gateways.GrantRequest{RoleID: ids[name], UserID: grants.Items[0].UserID, GatewayID: gateway.ID, Scope: "gateway"})
		if code, _ := requestJSON(t, "POST", root+"/role_bindings", owner, payload); code != 403 {
			t.Fatal("catalog role allowed escalation", name, code)
		}
	}
	stop()
	stop, address = startApplication(t, binary, f.dsn, config, settings...)
	root = address + "/api/hypershell/v1"
	if discoverRole(t, root, bearer, "gateway:owner").ID != ids["gateway:owner"] {
		t.Fatal("restart changed role identity")
	}
	// Invalid stored permissions must fail without a partial successful list.
	if _, err := f.db.Exec("ALTER TABLE roles DROP CONSTRAINT roles_permissions_object; UPDATE roles SET permissions='[]' WHERE name='gateway:viewer'"); err != nil {
		t.Fatal(err)
	}
	if code, _ := requestJSON(t, "GET", root+"/roles", bearer, nil); code != 500 {
		t.Fatal("invalid role metadata was hidden", code)
	}
}
