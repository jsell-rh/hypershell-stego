package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/segmentio/ksuid"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func TestGatewayUserMappingUsesSubjectAndOnlyTargetClient(t *testing.T) {
	id := ksuid.New().String()
	clientID, _ := GatewayClientID(id)
	var mu sync.Mutex
	current := []kcRole{{ID: "admin-role", Name: RoleAdmin}, {ID: "user-role", Name: RoleUser}}
	requests, writes := 0, 0
	failDelete := false
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		requests++
		switch {
		case r.URL.Path == "/realms/test/protocol/openid-connect/token":
			w.Write([]byte(`{"access_token":"fixture-admin","expires_in":300}`))
		case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients":
			json.NewEncoder(w).Encode([]kcClient{{ID: "client-uuid", ClientID: clientID}})
		case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients/client-uuid":
			json.NewEncoder(w).Encode(kcClient{ID: "client-uuid", ClientID: clientID, Attributes: map[string]string{gatewayAttribute: "true", gatewayIDAttribute: id}})
		case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients/client-uuid/roles":
			json.NewEncoder(w).Encode([]kcRole{{ID: "admin-role", Name: RoleAdmin}, {ID: "user-role", Name: RoleUser}})
		case r.Method == "GET" && r.URL.Path == "/admin/realms/test/users/provider-subject":
			w.Write([]byte(`{"id":"provider-subject","username":"unrelated-profile","enabled":true}`))
		case r.Method == "GET" && (r.URL.Path == "/admin/realms/test/users/provider-subject/role-mappings/clients/client-uuid" || r.URL.Path == "/admin/realms/test/users/provider-subject/role-mappings/clients/client-uuid/composite"):
			json.NewEncoder(w).Encode(current)
		case r.URL.Path == "/admin/realms/test/users/provider-subject/role-mappings/clients/client-uuid" && r.Method == "DELETE":
			writes++
			if failDelete {
				w.WriteHeader(503)
				return
			}
			var removed []kcRole
			if json.NewDecoder(r.Body).Decode(&removed) != nil {
				t.Error("invalid role payload")
				w.WriteHeader(400)
				return
			}
			for _, role := range removed {
				for i := len(current) - 1; i >= 0; i-- {
					if current[i].ID == role.ID {
						current = append(current[:i], current[i+1:]...)
					}
				}
			}
			w.WriteHeader(204)
		default:
			t.Errorf("role synchronization reached an unexpected endpoint: %s %s", r.Method, r.URL.Path)
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
	ctx := context.Background()
	if err := client.ReconcileGatewayUser(ctx, id, "https://other.example/realms/test", "provider-subject", "gateway:viewer"); err == nil {
		t.Fatal("mapped a foreign issuer")
	}
	mu.Lock()
	calls := requests
	mu.Unlock()
	if calls != 0 {
		t.Fatal("foreign issuer reached the provider")
	}
	if err := client.ReconcileGatewayUser(ctx, id, server.URL+"/realms/test", "provider-subject", "gateway:viewer"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	ok := len(current) == 1 && current[0].Name == RoleUser && writes == 1
	mu.Unlock()
	if !ok {
		t.Fatal("owner removal did not retain the viewer role")
	}
	if err := client.ReconcileGatewayUser(ctx, id, server.URL+"/realms/test", "provider-subject", "gateway:viewer"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	count := writes
	current = []kcRole{{ID: "unexpected", Name: "unexpected"}}
	failDelete = true
	mu.Unlock()
	if count != 1 {
		t.Fatal("unchanged roles caused a provider write")
	}
	if err := client.ReconcileGatewayUser(ctx, id, server.URL+"/realms/test", "provider-subject", "gateway:owner"); err == nil {
		t.Fatal("failed role removal reported success")
	}
}
