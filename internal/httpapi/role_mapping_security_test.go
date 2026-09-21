package httpapi

import (
	"bytes"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/application/responses"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

func roleMappingRow() model.Role {
	text := "shared text"
	instant := time.Date(2026, 9, 21, 0, 0, 0, 0, time.UTC)
	return model.Role{
		Meta: model.Meta{ID: "role-reference", CreatedTime: instant, UpdatedTime: instant},
		Name: "custom-role", DisplayName: &text, Description: &text,
		Permissions: []byte(`{"nested":{"values":["original"]}}`),
	}
}

func TestRESTRoleMappingOwnsValues(t *testing.T) {
	row := roleMappingRow()
	first, err := presentRole(row)
	if err != nil {
		t.Fatal(err)
	}
	second, err := presentRole(row)
	if err != nil {
		t.Fatal(err)
	}
	before := restObject(t, first)
	for _, field := range []*string{first.DisplayName, first.Description} {
		if field == nil || field == row.DisplayName || field == row.Description {
			t.Fatal("response text shares source storage")
		}
	}
	if first.DisplayName == first.Description {
		t.Fatal("response fields share storage")
	}
	*row.DisplayName = "source changed"
	copy(row.Permissions, bytes.Repeat([]byte{'x'}, len(row.Permissions)))
	if !reflect.DeepEqual(restObject(t, first), before) {
		t.Fatal("source changed the response")
	}
	*first.DisplayName = "response changed"
	(*first.Permissions)["nested"].(map[string]any)["values"].([]any)[0] = "response changed"
	if !reflect.DeepEqual(restObject(t, second), before) || *row.Description != "source changed" {
		t.Fatal("responses or source share storage")
	}
}

func TestRESTRoleMappingRejectsInvalidValues(t *testing.T) {
	bad := "private-role-" + string([]byte{255})
	cases := map[string]func(*model.Role){
		"id":             func(r *model.Role) { r.ID = bad },
		"name":           func(r *model.Role) { r.Name = bad },
		"display":        func(r *model.Role) { r.DisplayName = &bad },
		"description":    func(r *model.Role) { r.Description = &bad },
		"created_year":   func(r *model.Role) { r.CreatedTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) },
		"updated_offset": func(r *model.Role) { r.UpdatedTime = time.Now().In(time.FixedZone("private-offset", 61)) },
	}
	for name, raw := range map[string]string{
		"array": `[]`, "null": `null`, "string": `"private-role-value"`, "number": `1`,
		"whitespace": ` `, "truncated": `{"private-role-value":`, "trailing": `{} {}`,
		"duplicate":         `{"private-role-value":1,"private-role-value":2}`,
		"escaped_duplicate": `{"a":1,"\u0061":2}`,
		"surrogate":         `{"private-role-value":"\ud800"}`, "utf8": `{"key":"` + bad + `"}`,
		"bytes":        `{}` + strings.Repeat(" ", 262143),
		"nodes":        `{"list":[` + strings.Repeat("0,", 4093) + `0]}`,
		"depth":        `{"list":` + strings.Repeat("[", 16) + `0` + strings.Repeat("]", 16) + `}`,
		"string_bytes": `{"key":"` + strings.Repeat("a", 16385) + `"}`,
		"name_bytes":   `{"` + strings.Repeat("a", 16385) + `":null}`,
		"number_bytes": `{"key":` + strings.Repeat("1", 16385) + `}`,
	} {
		cases[name] = func(r *model.Role) { r.Permissions = []byte(raw) }
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			row := roleMappingRow()
			damage(&row)
			value, err := presentRole(row)
			if value != nil || err != responses.ErrConversion || err.Error() != "response conversion failed" {
				t.Fatal("invalid Role did not fail privately")
			}
			recorder := httptest.NewRecorder()
			writeError(recorder, httptest.NewRequest("GET", rolePath, nil), err)
			if recorder.Code != 500 || recorder.Header().Get("Cache-Control") != "no-store" || strings.Contains(recorder.Body.String(), "private-") {
				t.Fatal("Role conversion changed error status or disclosed stored data")
			}
		})
	}
}

func TestRESTRoleMappingAcceptsBounds(t *testing.T) {
	for name, raw := range map[string]string{
		"bytes":        `{}` + strings.Repeat(" ", 262142),
		"nodes":        `{"list":[` + strings.Repeat("0,", 4092) + `0]}`,
		"depth":        `{"list":` + strings.Repeat("[", 15) + `0` + strings.Repeat("]", 15) + `}`,
		"string_bytes": `{"key":"` + strings.Repeat("a", 16384) + `"}`,
		"name_bytes":   `{"` + strings.Repeat("a", 16384) + `":null}`,
		"number_bytes": `{"key":` + strings.Repeat("1", 16384) + `}`,
	} {
		t.Run(name, func(t *testing.T) {
			row := roleMappingRow()
			row.Permissions = []byte(raw)
			value, err := presentRole(row)
			if err != nil || value == nil || value.Permissions == nil {
				t.Fatal("valid Role object boundary rejected", err)
			}
		})
	}
	for _, raw := range [][]byte{nil, {}} {
		row := roleMappingRow()
		row.Permissions = raw
		value, err := presentRole(row)
		if err != nil || value.Permissions != nil {
			t.Fatal("absent Role permissions changed", err)
		}
	}
}
