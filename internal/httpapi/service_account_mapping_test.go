package httpapi

import (
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	"github.com/jsell-rh/hypershell-stego/out/application/responses"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

func restAccountRecord() model.ServiceAccount {
	description, lastError := "account description", "provider_unavailable"
	instant := time.Date(2026, 9, 21, 1, 2, 3, 456, time.FixedZone("offset", 3600))
	revoked := instant.Add(2 * time.Second)
	return model.ServiceAccount{
		Meta:      model.Meta{ID: "account-reference", CreatedTime: instant, UpdatedTime: instant.Add(time.Second)},
		GatewayID: "gateway-reference", Name: "automation", Description: &description,
		CredentialType: "client_secret", Role: "openshell-user", Status: "revoked",
		CreatedByUserID: "creator-reference", ClientID: "public-client", ClientUuid: "private-provider-id", Subject: "public-subject",
		ExpiresAt: instant.Add(time.Hour), RevokedAt: &revoked, LastError: &lastError,
	}
}

func TestRESTAccountMappingPreservesEveryField(t *testing.T) {
	for _, presence := range []string{"absent", "empty", "set"} {
		t.Run(presence, func(t *testing.T) {
			row := restAccountRecord()
			want := map[string]any{
				"id": "account-reference", "gateway_id": "gateway-reference", "name": "automation",
				"description": "account description", "credential_type": "client_secret", "role": "openshell-user", "status": "revoked",
				"created_by_user_id": "creator-reference", "client_id": "public-client", "subject": "public-subject",
				"expires_at": "2026-09-21T02:02:03.000000456+01:00", "revoked_at": "2026-09-21T01:02:05.000000456+01:00",
				"last_error": "provider_unavailable", "created_at": "2026-09-21T01:02:03.000000456+01:00", "updated_at": "2026-09-21T01:02:04.000000456+01:00",
			}
			switch presence {
			case "absent":
				row.Description, row.LastError, row.RevokedAt = nil, nil, nil
				want["description"], want["last_error"], want["revoked_at"] = nil, nil, nil
			case "empty":
				*row.Description, *row.LastError, *row.RevokedAt = "", "", time.Time{}
				want["description"], want["last_error"], want["revoked_at"] = "", "", "0001-01-01T00:00:00Z"
			}
			item, err := presentAccount(row)
			if err != nil {
				t.Fatal(err)
			}
			if got := restObject(t, item); !reflect.DeepEqual(got, want) {
				t.Fatalf("account contract changed: got %#v; want %#v", got, want)
			}
			// The application adds connection details only after successful conversion.
			create := accountCreateResponse{accountItem: *item, Credential: accountCredential{ClientSecret: "one-time-test-secret"}}
			get := accountGetResponse{accountItem: *item, Connection: serviceaccounts.Connection{}}
			for name, response := range map[string]any{"create": create, "get": get, "list": accountListResponse{Items: []accountItem{*item}}} {
				got := restObject(t, response)
				if name == "list" {
					got = got["items"].([]any)[0].(map[string]any)
				} else {
					extra := "connection"
					if name == "create" {
						extra = "credential"
					}
					if _, ok := got[extra]; !ok {
						t.Fatalf("%s data missing", name)
					}
					delete(got, extra)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s account fields changed", name)
				}
			}
		})
	}
}

func TestRESTAccountMappingAcceptsPublicEnums(t *testing.T) {
	for _, role := range []string{"openshell-user", "openshell-admin"} {
		for _, status := range []string{"provisioning", "ready", "degraded", "expired", "revoking", "revoked", "deleting", "error"} {
			t.Run(role+"/"+status, func(t *testing.T) {
				row := restAccountRecord()
				row.Role, row.Status = role, status
				item, err := presentAccount(row)
				if err != nil {
					t.Fatal(err)
				}
				got := restObject(t, item)
				if got["role"] != role || got["status"] != status || got["credential_type"] != "client_secret" {
					t.Fatal("public enum changed")
				}
			})
		}
	}
}

func TestRESTAccountMappingOwnsValues(t *testing.T) {
	row := restAccountRecord()
	first, err := presentAccount(row)
	if err != nil {
		t.Fatal(err)
	}
	before := restObject(t, first)
	second, err := presentAccount(row)
	if err != nil {
		t.Fatal(err)
	}
	*row.Description, *row.LastError, *row.RevokedAt = "changed", "changed", time.Time{}
	if got := restObject(t, first); !reflect.DeepEqual(got, before) {
		t.Fatal("response shares source storage")
	}
	first.Description.Set("new description")
	first.LastError.SetNull()
	first.RevokedAt.Set(time.Time{})
	if got := restObject(t, second); !reflect.DeepEqual(got, before) {
		t.Fatal("responses share nullable storage")
	}
	if *row.Description != "changed" || *row.LastError != "changed" || !row.RevokedAt.IsZero() {
		t.Fatal("response changed its source")
	}
	if got := restObject(t, first); got["description"] != "new description" || got["last_error"] != nil || got["revoked_at"] != "0001-01-01T00:00:00Z" {
		t.Fatal("nullable fields share storage")
	}
}

func TestRESTAccountMappingRejectsInvalidValues(t *testing.T) {
	bad := "private-invalid-" + string([]byte{255})
	cases := map[string]func(*model.ServiceAccount){
		"id":                 func(r *model.ServiceAccount) { r.ID = bad },
		"gateway_id":         func(r *model.ServiceAccount) { r.GatewayID = bad },
		"name":               func(r *model.ServiceAccount) { r.Name = bad },
		"description":        func(r *model.ServiceAccount) { r.Description = &bad },
		"credential_type":    func(r *model.ServiceAccount) { r.CredentialType = "private-unknown-credential" },
		"role":               func(r *model.ServiceAccount) { r.Role = "private-unknown-role" },
		"status":             func(r *model.ServiceAccount) { r.Status = "private-unknown-status" },
		"created_by_user_id": func(r *model.ServiceAccount) { r.CreatedByUserID = bad },
		"client_id":          func(r *model.ServiceAccount) { r.ClientID = bad },
		"subject":            func(r *model.ServiceAccount) { r.Subject = bad },
		"last_error":         func(r *model.ServiceAccount) { r.LastError = &bad },
	}
	for name, set := range map[string]func(*model.ServiceAccount, time.Time){
		"created_at": func(r *model.ServiceAccount, v time.Time) { r.CreatedTime = v },
		"updated_at": func(r *model.ServiceAccount, v time.Time) { r.UpdatedTime = v },
		"expires_at": func(r *model.ServiceAccount, v time.Time) { r.ExpiresAt = v },
		"revoked_at": func(r *model.ServiceAccount, v time.Time) { r.RevokedAt = &v },
	} {
		for variant, instant := range map[string]time.Time{
			"year":   time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC),
			"offset": time.Date(2026, 1, 1, 0, 0, 0, 0, time.FixedZone("private-offset", 61)),
		} {
			cases[name+"/"+variant] = func(r *model.ServiceAccount) { set(r, instant) }
		}
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			row := restAccountRecord()
			damage(&row)
			item, err := presentAccount(row)
			if item != nil || err != responses.ErrConversion {
				t.Fatal("invalid account did not fail privately")
			}
			recorder := httptest.NewRecorder()
			accountError(recorder, httptest.NewRequest("GET", "/", nil), err)
			if recorder.Code != 500 || strings.Contains(recorder.Body.String(), "private-") || recorder.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("conversion error disclosed data or changed status")
			}
			if got := recorder.Body.String(); got != "{\"code\":\"internal_error\",\"reason\":\"The request could not be completed\"}\n" {
				t.Fatal("conversion error shape changed")
			}
		})
	}
}
