package httpapi

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

func TestRESTRoleMappingPreservesEveryField(t *testing.T) {
	for _, presence := range []string{"absent", "empty", "set"} {
		t.Run(presence, func(t *testing.T) {
			display, description := "Role display", "Role description"
			instant := time.Date(2026, 9, 21, 1, 2, 3, 456, time.FixedZone("offset", 3600))
			row := model.Role{
				Meta: model.Meta{ID: "role-reference", CreatedTime: instant, UpdatedTime: instant.Add(time.Second)},
				Name: "custom-role", DisplayName: &display, Description: &description,
				Permissions: []byte(`{"gateways":["read"]}`), BuiltIn: true,
			}
			want := map[string]any{
				"id": "role-reference", "kind": "Role", "href": "/api/hypershell/v1/roles/role-reference",
				"created_at": "2026-09-21T01:02:03.000000456+01:00", "updated_at": "2026-09-21T01:02:04.000000456+01:00",
				"name": "custom-role", "display_name": display, "description": description,
				"permissions": map[string]any{"gateways": []any{"read"}}, "built_in": true,
			}
			switch presence {
			case "absent":
				row.DisplayName, row.Description, row.Permissions = nil, nil, nil
				for _, key := range []string{"display_name", "description", "permissions"} {
					delete(want, key)
				}
			case "empty":
				display, description = "", ""
				row.Permissions, row.BuiltIn = []byte(`{}`), false
				want["display_name"], want["description"], want["permissions"], want["built_in"] = "", "", map[string]any{}, false
			}
			value, err := presentRole(row)
			if err != nil {
				t.Fatal(err)
			}
			if got := restObject(t, value); !reflect.DeepEqual(got, want) {
				t.Fatalf("Role fields changed: got %#v; want %#v", got, want)
			}
		})
	}
}

func TestRESTRoleMappingPreservesObjectValues(t *testing.T) {
	permissions := []byte(`{"integer":9007199254740993,"decimal":1.234567890123456789,"exponent":1e999,"negative_zero":-0,"nested":{"items":[null,false,{},[],"text"]},"unicode":"\ud83d\ude00"}`)
	value, err := presentRole(model.Role{Name: "custom-role", Permissions: permissions})
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	decode := func(data []byte) map[string]any {
		t.Helper()
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.UseNumber()
		var object map[string]any
		if err := decoder.Decode(&object); err != nil {
			t.Fatal(err)
		}
		return object
	}
	got := decode(body)["permissions"]
	if !reflect.DeepEqual(got, decode(permissions)) {
		t.Fatalf("Role object values changed: %#v", got)
	}
}
