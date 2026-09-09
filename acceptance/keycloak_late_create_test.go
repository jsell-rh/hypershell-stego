package acceptance

import (
	"context"
	"database/sql"
	"fmt"
	"net/http"
	"testing"
	"time"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
)

func TestLateKeycloakCreationIsRemovedAfterCleanupAndRestart(t *testing.T) {
	k := startKeycloak(t)
	delayed, arm, entered, release, completed := delayedKeycloak(t, k, "create")
	defer release()
	f := database(t)
	_, gateway := accountService(t, f, newAccountProvider())
	k.bindGateway(t, "gateway-audience", gateway.ID)
	oidc := fmt.Sprintf(`{"issuer":%q,"client_id":"gateway-audience","audience":"gateway-audience"}`, delayed.options.ServerURL+"/realms/workflow")
	if _, err := f.db.Exec("UPDATE gateways SET oidc=$1 WHERE id=$2", oidc, gateway.ID); err != nil {
		t.Fatal(err)
	}
	observeGatewayFixture(t, f, gateway.ID)
	key, settings := issuer(t)
	providerSettings, _ := startRealProvisioner(t, delayed, key, settings)
	settings = append(settings, providerSettings...)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	path := "/api/hypershell/v1/gateways/" + gateway.ID + "/service_accounts"
	owner := token(t, key, "alice")
	arm.Store(true)
	result := make(chan int, 1)
	go func() {
		code, _ := requestJSON(t, "POST", address+path, owner, []byte(`{"name":"late-create"}`))
		result <- code
	}()
	select {
	case <-entered:
	case <-time.After(15 * time.Second):
		t.Fatal("creation did not reach the provider")
	}
	select {
	case code := <-result:
		if code != http.StatusServiceUnavailable {
			t.Fatalf("delayed creation returned %d", code)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("creation did not finish after its deadline")
	}
	var id, clientID string
	var deleted sql.NullTime
	if err := f.db.QueryRow("SELECT id,client_id,deleted_at FROM service_accounts WHERE gateway_id=$1 AND name='late-create'", gateway.ID).Scan(&id, &clientID, &deleted); err != nil {
		t.Fatal(err)
	}
	if !deleted.Valid {
		t.Fatal("initial cleanup did not remove the failed reservation")
	}
	// Cleanup must not depend on a live parent Gateway after a terminal action.
	code, _ := requestJSON(t, "DELETE", address+"/api/hypershell/v1/gateways/"+gateway.ID, owner, nil)
	if code != 204 {
		t.Fatalf("delete Gateway after failed creation: %d", code)
	}
	stop()
	release()
	select {
	case code := <-completed:
		if code != http.StatusCreated {
			t.Fatalf("late provider creation returned %d", code)
		}
	case <-time.After(12 * time.Second):
		t.Fatal("late creation did not finish")
	}
	provider, err := keycloak.NewClient(k.options)
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close()
	clients, err := provider.ListManagedClients(context.Background(), gateway.ID)
	if err != nil || len(clients) != 1 || clients[0].ClientID != clientID {
		t.Fatalf("late client was not created: count %d, error %v", len(clients), err)
	}
	stop, _ = startApplication(t, binary, f.dsn, config, settings...)
	defer stop()
	deadline := time.Now().Add(20 * time.Second)
	for {
		clients, err = provider.ListManagedClients(context.Background(), gateway.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(clients) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("late provider client survived cleanup and restart")
		}
		time.Sleep(250 * time.Millisecond)
	}
	if err := f.db.QueryRow("SELECT deleted_at FROM service_accounts WHERE id=$1", id).Scan(&deleted); err != nil || !deleted.Valid {
		t.Fatal("recovery restored deleted metadata")
	}
	var audits int
	if err := f.db.QueryRow("SELECT count(*) FROM service_account_audits WHERE service_account_id=$1", id).Scan(&audits); err != nil || audits == 0 {
		t.Fatal("recovery lost the account audit")
	}
}
