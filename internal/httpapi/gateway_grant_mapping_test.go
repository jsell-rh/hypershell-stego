package httpapi

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/application/responses"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

func restGatewayRecord() model.Gateway {
	text := func(s string) *string { return &s }
	count := int32(7)
	instant := time.Date(2026, 9, 21, 1, 2, 3, 456, time.FixedZone("offset", 3600))
	return model.Gateway{Meta: model.Meta{ID: "gateway-reference", CreatedTime: instant, UpdatedTime: instant.Add(time.Second)},
		ResourceGeneration: 3, ObservedGenerations: []byte(`{"console":3,"endpoint":3,"workload":3}`),
		Name: "gateway", ClusterID: "cluster-reference", ReleaseID: "release-reference", Namespace: "gateway-namespace",
		ExternalDns: text("gateway.example.test"), TlsMode: text("passthrough"), ServiceType: text("ClusterIP"), Status: text("Ready"), Phase: text("Ready"), Image: text("gateway-image"), SupervisorImage: text("supervisor-image"),
		ServerDnsNames: []byte(`["second.example.test","first.example.test","second.example.test"]`), RouteAddress: text("https://gateway.example.test"), ConsoleAddress: text("https://console.example.test"), Oidc: text("oidc"), Route: text("route"), CredentialDriver: text("postgres"), ActiveSandboxCount: &count}
}
func restObject(t *testing.T, value any) map[string]any {
	t.Helper()
	body, e := json.Marshal(value)
	if e != nil {
		t.Fatal(e)
	}
	var got map[string]any
	if e = json.Unmarshal(body, &got); e != nil {
		t.Fatal(e)
	}
	return got
}
func TestRESTGatewayMappingPreservesEveryField(t *testing.T) {
	for _, presence := range []string{"absent", "empty", "set"} {
		t.Run(presence, func(t *testing.T) {
			row := restGatewayRecord()
			creator := "resolved-creator"
			fields := []**string{&row.ExternalDns, &row.TlsMode, &row.ServiceType, &row.Status, &row.Phase, &row.Image, &row.SupervisorImage, &row.RouteAddress, &row.ConsoleAddress, &row.Oidc, &row.Route, &row.CredentialDriver}
			optional := map[string]any{"external_dns": "gateway.example.test", "tls_mode": "passthrough", "service_type": "ClusterIP", "status": "Ready", "phase": "Ready", "image": "gateway-image", "supervisor_image": "supervisor-image", "route_address": "https://gateway.example.test", "console_address": "https://console.example.test", "oidc": "oidc", "route": "route", "credential_driver": "postgres", "active_sandbox_count": float64(7)}
			if presence == "absent" {
				for _, f := range fields {
					*f = nil
				}
				row.ActiveSandboxCount = nil
				row.ServerDnsNames = nil
				creator = ""
			}
			if presence == "empty" {
				for _, f := range fields {
					v := ""
					*f = &v
				}
				n := int32(0)
				row.ActiveSandboxCount = &n
				row.ServerDnsNames = []byte(`[]`)
				creator = ""
			}
			got, e := present(row, creator)
			if e != nil {
				t.Fatal(e)
			}
			want := map[string]any{"id": "gateway-reference", "kind": "Gateway", "href": "/api/hypershell/v1/gateways/gateway-reference", "created_at": "2026-09-21T01:02:03.000000456+01:00", "updated_at": "2026-09-21T01:02:04.000000456+01:00", "name": "gateway", "cluster_id": "cluster-reference", "release_id": "release-reference", "namespace": "gateway-namespace"}
			if presence != "absent" {
				for k, v := range optional {
					if presence == "empty" {
						if _, number := v.(float64); number {
							v = float64(0)
						} else {
							v = ""
						}
					}
					want[k] = v
				}
			}
			if presence == "set" {
				want["created_by"] = "resolved-creator"
				want["server_dns_names"] = []any{"second.example.test", "first.example.test", "second.example.test"}
			}
			if actual := restObject(t, got); !reflect.DeepEqual(actual, want) {
				t.Fatalf("Gateway fields changed: got %#v; want %#v", actual, want)
			}
		})
	}
}
func TestRESTGatewayMappingSelectsObservations(t *testing.T) {
	for _, deleting := range []bool{false, true} {
		row := restGatewayRecord()
		bad := string([]byte{255})
		row.Status = &bad
		row.Phase = &bad
		if deleting {
			row.DeletedAt.Valid = true
			row.DeletedAt.Time = row.UpdatedTime
		} else {
			row.ResourceGeneration = 4
			row.RouteAddress = &bad
			row.ConsoleAddress = &bad
		}
		got, e := present(row, "creator")
		if e != nil {
			t.Fatal(e)
		}
		wantPhase, wantStatus := "Provisioning", "ObservationPending"
		if deleting {
			wantPhase, wantStatus = "Deleting", "Gateway cleanup is in progress"
		}
		if got.Phase == nil || *got.Phase != wantPhase || got.Status == nil || *got.Status != wantStatus {
			t.Fatal("domain observation selection changed")
		}
		if !deleting && (got.RouteAddress == nil || *got.RouteAddress != "" || got.ConsoleAddress == nil || *got.ConsoleAddress != "") {
			t.Fatal("stale endpoints were returned")
		}
		if *row.Status != bad || *row.Phase != bad {
			t.Fatal("conversion changed stored observations")
		}
	}
}
func TestRESTGatewayMappingOwnsStorage(t *testing.T) {
	row := restGatewayRecord()
	got, e := present(row, "creator")
	if e != nil {
		t.Fatal(e)
	}
	source := []*string{row.ExternalDns, row.TlsMode, row.ServiceType, row.Status, row.Phase, row.Image, row.SupervisorImage, row.RouteAddress, row.ConsoleAddress, row.Oidc, row.Route, row.CredentialDriver}
	output := []*string{got.ExternalDns, got.TlsMode, got.ServiceType, got.Status, got.Phase, got.Image, got.SupervisorImage, got.RouteAddress, got.ConsoleAddress, got.Oidc, got.Route, got.CredentialDriver}
	for i, p := range output {
		if p == nil || p == source[i] {
			t.Fatal("response shares source storage")
		}
		old := *source[i]
		*p = "response"
		if *source[i] != old {
			t.Fatal("response changed source")
		}
		*source[i] = "source"
		if *p != "response" {
			t.Fatal("source changed response")
		}
	}
	*got.ActiveSandboxCount = 9
	if *row.ActiveSandboxCount != 7 {
		t.Fatal("response shares number")
	}
	(*got.ServerDnsNames)[0] = "response"
	if !strings.Contains(string(row.ServerDnsNames), "second.example.test") {
		t.Fatal("response changed JSON source")
	}
	for i := range row.ServerDnsNames {
		row.ServerDnsNames[i] = 'x'
	}
	if (*got.ServerDnsNames)[1] != "first.example.test" {
		t.Fatal("source changed response list")
	}
}
func TestRESTGatewayMappingRejectsInvalidValues(t *testing.T) {
	cases := map[string]string{"malformed": "[", "object": "{}", "null member": "[null]", "number": "[17]", "nested": "[[]]", "trailing": "[] null", "encoding": string([]byte{'[', '"', 255, '"', ']'}), "surrogate": `["\uD800"]`, "input bytes": strings.Repeat(" ", 262143) + "[]", "item bytes": `["` + strings.Repeat("x", 254) + `"]`}
	names := make([]string, 129)
	for i := range names {
		names[i] = "gateway.example.test"
	}
	raw, e := json.Marshal(names)
	if e != nil {
		t.Fatal(e)
	}
	cases["count"] = string(raw)
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			row := restGatewayRecord()
			row.ServerDnsNames = []byte(raw)
			got, e := present(row, "creator")
			if got != nil || e != responses.ErrConversion || e.Error() != "response conversion failed" {
				t.Fatal("invalid list returned data or changed its error")
			}
		})
	}
	bad := string([]byte{255})
	for name, edit := range map[string]func(*model.Gateway, *string){"creator": func(r *model.Gateway, c *string) { *c = bad }, "name": func(r *model.Gateway, c *string) { r.Name = bad }, "observation": func(r *model.Gateway, c *string) { r.Status = &bad }, "timestamp": func(r *model.Gateway, c *string) { r.CreatedTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }} {
		t.Run(name, func(t *testing.T) {
			row := restGatewayRecord()
			creator := "creator"
			edit(&row, &creator)
			got, e := present(row, creator)
			if got != nil || e != responses.ErrConversion {
				t.Fatal("invalid scalar returned data")
			}
		})
	}
}
func TestRESTGatewayMappingAcceptsListBounds(t *testing.T) {
	row := restGatewayRecord()
	names := make([]string, 128)
	for i := range names {
		names[i] = strings.Repeat("<", 253)
	}
	raw, e := json.Marshal(names)
	if e != nil {
		t.Fatal(e)
	}
	row.ServerDnsNames = raw
	got, e := present(row, "")
	if e != nil || got.ServerDnsNames == nil || !reflect.DeepEqual(*got.ServerDnsNames, names) {
		t.Fatal("valid bounds were rejected", e)
	}
	for _, raw := range [][]byte{nil, []byte(`null`), []byte(`[]`)} {
		row.ServerDnsNames = raw
		got, e = present(row, "")
		if e != nil || got.ServerDnsNames != nil {
			t.Fatal("empty list presence changed", e)
		}
	}
}
func TestRESTGrantMappingPreservesFieldsAndOwnership(t *testing.T) {
	instant := time.Date(2026, 9, 21, 1, 2, 3, 456, time.UTC)
	gateway := "gateway-reference"
	for _, scope := range []string{"gateway", "global"} {
		row := model.RoleBinding{Meta: model.Meta{ID: "grant-reference", CreatedTime: instant, UpdatedTime: instant}, UserID: "user-reference", RoleID: "role-reference", Scope: scope}
		if scope == "gateway" {
			row.GatewayID = &gateway
		}
		got, e := presentGrant(row)
		if e != nil {
			t.Fatal(e)
		}
		want := map[string]any{"id": "grant-reference", "kind": "RoleBinding", "href": "/api/hypershell/v1/role_bindings/grant-reference", "created_at": "2026-09-21T01:02:03.000000456Z", "updated_at": "2026-09-21T01:02:03.000000456Z", "user_id": "user-reference", "role_id": "role-reference", "scope": scope}
		if scope == "gateway" {
			want["gateway_id"] = gateway
		}
		if !reflect.DeepEqual(restObject(t, got), want) {
			t.Fatal("grant fields changed")
		}
		if got.GatewayId != nil {
			if got.GatewayId == row.GatewayID {
				t.Fatal("grant shares pointer")
			}
			*got.GatewayId = "changed"
			if gateway != "gateway-reference" {
				t.Fatal("response changed grant source")
			}
		}
	}
}
func TestRESTGrantMappingRejectsInvalidValues(t *testing.T) {
	for name, edit := range map[string]func(*model.RoleBinding){"scope": func(r *model.RoleBinding) { r.Scope = "unknown" }, "empty scope": func(r *model.RoleBinding) { r.Scope = "" }, "encoding": func(r *model.RoleBinding) { r.UserID = string([]byte{255}) }, "timestamp": func(r *model.RoleBinding) { r.UpdatedTime = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC) }} {
		t.Run(name, func(t *testing.T) {
			row := model.RoleBinding{Scope: "gateway"}
			edit(&row)
			got, e := presentGrant(row)
			if got != nil || e != responses.ErrConversion || e.Error() != "response conversion failed" {
				t.Fatal("invalid grant returned data or changed its error")
			}
		})
	}
}
