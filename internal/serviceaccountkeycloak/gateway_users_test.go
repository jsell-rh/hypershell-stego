package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	provider "github.com/jsell-rh/hypershell-stego/out/keycloak"
	"github.com/segmentio/ksuid"
)

func TestGatewayUserMappingUsesSubjectAndOnlyTargetClient(t *testing.T) {
	for _, tc := range []struct {
		managed                  bool
		name, role, user, issuer string
		initial, inherited       []string
		failDelete, changeOwner  bool
		wantError                bool
		wantRoles                []string
		wantWrites               string
	}{
		{name: "person owner", role: "gateway:owner", wantRoles: []string{RoleAdmin, RoleUser}, wantWrites: "POST"},
		{name: "automation owner", role: "gateway:owner", user: `{"id":"provider-subject","enabled":true,"serviceAccountClientId":"registered-automation"}`, wantRoles: []string{RoleAdmin, RoleUser}, wantWrites: "POST"},
		{name: "automation viewer", role: "gateway:viewer", user: `{"id":"provider-subject","enabled":true,"serviceAccountClientId":"registered-automation"}`, wantRoles: []string{RoleUser}, wantWrites: "POST"},
		{name: "migrated owner", managed: true, role: "gateway:owner", wantRoles: []string{RoleAdmin, RoleUser}, wantWrites: "POST"},
		{name: "migrated automation viewer", managed: true, role: "gateway:viewer", user: `{"id":"provider-subject","enabled":true,"serviceAccountClientId":"automation"}`, wantRoles: []string{RoleUser}, wantWrites: "POST"},
		{name: "owner to viewer", role: "gateway:viewer", initial: []string{RoleAdmin, RoleUser}, wantRoles: []string{RoleUser}, wantWrites: "DELETE"},
		{name: "viewer unchanged", role: "gateway:viewer", initial: []string{RoleUser}, wantRoles: []string{RoleUser}},
		{name: "remove all Gateway roles", initial: []string{RoleAdmin, RoleUser}, wantWrites: "DELETE"},
		{name: "disabled identity revoke", user: `{"id":"provider-subject","enabled":false}`, initial: []string{RoleUser}, wantWrites: "DELETE"},
		{name: "disabled identity grant", role: "gateway:viewer", user: `{"id":"provider-subject","enabled":false}`, wantError: true},
		{name: "missing enabled flag", role: "gateway:viewer", user: `{"id":"provider-subject"}`, wantError: true},
		{name: "wrong subject", role: "gateway:viewer", user: `{"id":"different-subject","enabled":true}`, wantError: true},
		{name: "missing identity revoke", user: "missing"},
		{name: "missing identity grant", user: "missing", role: "gateway:viewer", wantError: true},
		{name: "foreign issuer", issuer: "https://other.example/realms/test", role: "gateway:viewer", wantError: true},
		{name: "invalid role", role: "platform-admin", wantError: true},
		{name: "remove before add", role: "gateway:viewer", initial: []string{"unexpected"}, wantRoles: []string{RoleUser}, wantWrites: "DELETE,POST"},
		{name: "failed removal prevents addition", role: "gateway:owner", initial: []string{"unexpected"}, failDelete: true, wantError: true, wantRoles: []string{"unexpected"}, wantWrites: "DELETE"},
		{name: "inherited excess prevents addition", role: "gateway:viewer", inherited: []string{RoleAdmin}, wantError: true},
		{name: "inherited excess blocks completed revoke", initial: []string{RoleAdmin}, inherited: []string{RoleAdmin}, wantError: true, wantWrites: "DELETE"},
		{name: "changed owner prevents addition", role: "gateway:viewer", changeOwner: true, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			id := ksuid.New().String()
			clientID, _ := GatewayClientID(id)
			makeRoles := func(names []string) []provider.RoleRepresentation {
				roles := make([]provider.RoleRepresentation, 0, len(names))
				for _, name := range names {
					roles = append(roles, provider.RoleRepresentation{ID: name + "-id", Name: name, ClientRole: true, ContainerID: "client-uuid"})
				}
				return roles
			}
			var mu sync.Mutex
			current := makeRoles(tc.initial)
			var writes []string
			requests, bindingReads := 0, 0
			userPath := "/admin/realms/test/users/provider-subject"
			mappingPath := userPath + "/role-mappings/clients/client-uuid"
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				requests++
				switch {
				case r.Method == "POST" && r.URL.Path == "/realms/test/protocol/openid-connect/token":
					w.Write([]byte(`{"access_token":"fixture-admin","expires_in":300,"token_type":"Bearer"}`))
				case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients":
					json.NewEncoder(w).Encode([]kcClient{{ID: "client-uuid", ClientID: clientID}})
				case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients/client-uuid":
					bindingReads++
					owner := id
					if tc.changeOwner && bindingReads > 1 {
						owner = "other-gateway"
					}
					attributes := map[string]string{gatewayAttribute: "true", gatewayIDAttribute: owner}
					if tc.managed {
						attributes = map[string]string{managedGatewayAttribute: "true", managedGatewayIDAttribute: owner}
					}
					json.NewEncoder(w).Encode(kcClient{ID: "client-uuid", ClientID: clientID, Attributes: attributes})
				case r.Method == "GET" && strings.HasPrefix(r.URL.Path, "/admin/realms/test/clients/client-uuid/roles/"):
					name := strings.TrimPrefix(r.URL.Path, "/admin/realms/test/clients/client-uuid/roles/")
					if name != RoleAdmin && name != RoleUser {
						t.Errorf("unexpected desired role: %s", name)
						w.WriteHeader(404)
						return
					}
					json.NewEncoder(w).Encode(makeRoles([]string{name})[0])
				case r.Method == "GET" && r.URL.Path == userPath:
					if tc.user == "missing" {
						w.WriteHeader(404)
					} else if tc.user != "" {
						w.Write([]byte(tc.user))
					} else {
						w.Write([]byte(`{"id":"provider-subject","username":"unrelated-profile","enabled":true}`))
					}
				case r.Method == "GET" && r.URL.Path == mappingPath:
					json.NewEncoder(w).Encode(current)
				case r.Method == "GET" && r.URL.Path == mappingPath+"/composite":
					effective := append(makeRoles(tc.inherited), current...)
					json.NewEncoder(w).Encode(effective)
				case r.URL.Path == mappingPath && (r.Method == "DELETE" || r.Method == "POST"):
					writes = append(writes, r.Method)
					if r.Method == "DELETE" && tc.failDelete {
						w.WriteHeader(503)
						return
					}
					var changed []kcRole
					if json.NewDecoder(r.Body).Decode(&changed) != nil {
						t.Error("invalid role payload")
						w.WriteHeader(400)
						return
					}
					for _, role := range changed {
						if role.ID != role.Name+"-id" {
							t.Error("role ID does not match its name")
						}
						if r.Method == "POST" {
							current = append(current, makeRoles([]string{role.Name})[0])
						} else {
							for i := len(current) - 1; i >= 0; i-- {
								if current[i].ID == role.ID {
									current = append(current[:i], current[i+1:]...)
								}
							}
						}
					}
					w.WriteHeader(204)
				default:
					t.Errorf("unexpected endpoint: %s %s", r.Method, r.URL.Path)
					w.WriteHeader(500)
				}
			}))
			defer server.Close()
			ca, secret := filepath.Join(t.TempDir(), "ca"), filepath.Join(t.TempDir(), "secret")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(secret, []byte("fixture-secret"), 0600); err != nil {
				t.Fatal(err)
			}
			client, err := NewClient(Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			issuer := server.URL + "/realms/test"
			if tc.issuer != "" {
				issuer = tc.issuer
			}
			err = client.ReconcileGatewayUser(context.Background(), id, issuer, "provider-subject", tc.role)
			if (err != nil) != tc.wantError {
				t.Fatalf("unexpected result: %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if strings.Join(writes, ",") != tc.wantWrites {
				t.Errorf("writes %v; want %s", writes, tc.wantWrites)
			}
			if len(current) != len(tc.wantRoles) {
				t.Fatalf("roles %v; want %v", current, tc.wantRoles)
			}
			for _, name := range tc.wantRoles {
				found := false
				for _, role := range current {
					found = found || role.Name == name
				}
				if !found {
					t.Errorf("missing role %s", name)
				}
			}
			if (tc.issuer != "" || tc.role == "platform-admin") && requests != 0 {
				t.Error("invalid application policy reached the provider")
			}
		})
	}
}
