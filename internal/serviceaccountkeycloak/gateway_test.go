package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/segmentio/ksuid"
)

func TestGatewayClientCannotAdoptForeignIdentity(t *testing.T) {
	id := ksuid.New().String()
	clientID, _ := GatewayClientID(id)
	for _, attributes := range []map[string]string{nil, {gatewayAttribute: "true", gatewayIDAttribute: ksuid.New().String()}, {gatewayIDAttribute: id}} {
		var writes atomic.Int32
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch {
			case r.URL.Path == "/realms/test/protocol/openid-connect/token":
				w.Write([]byte(`{"access_token":"test-admin-token","expires_in":300}`))
			case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients":
				json.NewEncoder(w).Encode([]kcClient{{ID: "uuid", ClientID: clientID}})
			case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients/uuid":
				json.NewEncoder(w).Encode(kcClient{ID: "uuid", ClientID: clientID, Attributes: attributes})
			default:
				writes.Add(1)
				w.WriteHeader(500)
			}
		}))
		ca, secret := filepath.Join(t.TempDir(), "ca"), filepath.Join(t.TempDir(), "secret")
		if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(secret, []byte("test-secret"), 0600); err != nil {
			t.Fatal(err)
		}
		client, err := NewClient(Options{ServerURL: server.URL, Realm: "test", ClientID: "admin", SecretFile: secret, CAFile: ca})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.EnsureGateway(context.Background(), id, "gateway"); err == nil {
			t.Fatal("adopted an untrusted Gateway client")
		}
		if err := client.DeleteGateway(context.Background(), id); err == nil {
			t.Fatal("deleted an untrusted Gateway client")
		}
		if writes.Load() != 0 {
			t.Fatal("invalid Gateway binding reached provider configuration")
		}
		client.Close()
		server.Close()
	}
}
