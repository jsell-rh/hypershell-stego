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
		if err := client.ReconcileGatewayUser(context.Background(), id, server.URL+"/realms/test", "subject", "gateway:owner"); err == nil {
			t.Fatal("mapped a user to an untrusted Gateway")
		}
		if writes.Load() != 0 {
			t.Fatal("invalid Gateway binding reached provider configuration")
		}
		client.Close()
		server.Close()
	}
}

func TestGatewayDeletionRequiresConfirmedAbsence(t *testing.T) {
	id := ksuid.New().String()
	clientID, _ := GatewayClientID(id)
	var present atomic.Bool
	present.Store(true)
	var deletes atomic.Int32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/realms/test/protocol/openid-connect/token":
			w.Write([]byte(`{"access_token":"test-admin-token","expires_in":300}`))
		case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients":
			clients := []kcClient{}
			if present.Load() {
				clients = append(clients, kcClient{ID: "uuid", ClientID: clientID})
			}
			json.NewEncoder(w).Encode(clients)
		case r.Method == "GET" && r.URL.Path == "/admin/realms/test/clients/uuid":
			json.NewEncoder(w).Encode(kcClient{ID: "uuid", ClientID: clientID, Attributes: map[string]string{gatewayAttribute: "true", gatewayIDAttribute: id}})
		case r.Method == "DELETE" && r.URL.Path == "/admin/realms/test/clients/uuid":
			deletes.Add(1)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusInternalServerError)
		}
	}))
	defer server.Close()
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
	defer client.Close()
	if err := client.DeleteGateway(context.Background(), id); err == nil || deletes.Load() != 1 {
		t.Fatal("delete response was treated as absence", err)
	}
	present.Store(false)
	if err := client.DeleteGateway(context.Background(), id); err != nil || deletes.Load() != 1 {
		t.Fatal("confirmed absence failed", err)
	}
}
