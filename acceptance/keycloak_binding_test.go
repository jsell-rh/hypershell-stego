package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/auth"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// Only the trusted fixture administrator can set provider ownership metadata.
func (k *keycloakFixture) bindGateway(t *testing.T, clientID, gatewayID string) {
	t.Helper()
	response, grant := k.issue(t, "provisioner", "acceptance-only-admin-secret")
	if response.StatusCode != 200 {
		t.Fatal("cannot obtain fixture administrator token")
	}
	headers := http.Header{"Authorization": {"Bearer " + grant["access_token"].(string)}, "Content-Type": {"application/json"}}
	response, err := k.http.Do(context.Background(), "GET", "/admin/realms/workflow/clients?clientId="+url.QueryEscape(clientID), headers, nil)
	var clients []struct {
		ID string `json:"id"`
	}
	if err != nil || response.StatusCode != 200 || json.Unmarshal(response.Body, &clients) != nil || len(clients) != 1 {
		t.Fatal("cannot resolve fixture Gateway client")
	}
	body, err := json.Marshal(map[string]any{"attributes": map[string]string{"hypershell.gateway": "true", "hypershell.gateway-id": gatewayID}})
	if err != nil {
		t.Fatal(err)
	}
	response, err = k.http.Do(context.Background(), "PUT", "/admin/realms/workflow/clients/"+clients[0].ID, headers, body)
	if err != nil || response.StatusCode != 204 {
		t.Fatal("cannot bind fixture Gateway client")
	}
}

func TestKeycloakGatewayAudienceBinding(t *testing.T) {
	k := startKeycloak(t)
	f := database(t)
	_, gateway := accountService(t, f, newAccountProvider())
	k.bindGateway(t, "gateway-audience", gateway.ID)
	oidc := fmt.Sprintf(`{"issuer":%q,"client_id":"gateway-audience","audience":"gateway-audience"}`, k.options.ServerURL+"/realms/workflow")
	if _, err := f.db.Exec("UPDATE gateways SET oidc=$1 WHERE id=$2", oidc, gateway.ID); err != nil {
		t.Fatal(err)
	}
	observeGatewayFixture(t, f, gateway.ID)
	key, settings := issuer(t)
	providerSettings, _ := startRealProvisioner(t, k, key, settings)
	settings = append(settings, providerSettings...)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	defer stop()
	root := address + "/api/hypershell/v1/gateways"
	owner := token(t, key, "alice")
	other := token(t, key, "bob", "gateway:creator")
	keys, err := k.http.Do(context.Background(), "GET", "/realms/workflow/protocol/openid-connect/certs", nil, nil)
	if err != nil || keys.StatusCode != 200 {
		t.Fatal("cannot read provider verification keys")
	}
	t.Run("foreign audience", func(t *testing.T) {
		request := f.request("other-gateway")
		request.OIDC = &oidc
		body, err := json.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		code, body := requestJSON(t, "POST", root, other, body)
		var otherGateway struct {
			ID string `json:"id"`
		}
		if code != 201 || json.Unmarshal(body, &otherGateway) != nil || otherGateway.ID == "" {
			t.Fatalf("create second Gateway: %d", code)
		}
		observeGatewayFixture(t, f, otherGateway.ID)
		if code, _ := requestJSON(t, "GET", root+"/"+gateway.ID, other, nil); code != 404 {
			t.Fatalf("second owner can read first Gateway: %d", code)
		}
		path := root + "/" + otherGateway.ID + "/service_accounts"
		code, body = requestJSON(t, "POST", path, other, []byte(`{"name":"foreign-admin","role":"openshell-admin"}`))
		if code == 201 {
			var created struct {
				ID         string `json:"id"`
				Credential struct {
					Secret string `json:"client_secret"`
				} `json:"credential"`
			}
			// Read the stored client ID. The failure output never includes a credential.
			if json.Unmarshal(body, &created) != nil {
				t.Fatal("invalid account response")
			}
			stored, err := f.storage.Get(context.Background(), "ServiceAccount", created.ID)
			if err != nil {
				t.Fatal(err)
			}
			row := stored.(model.ServiceAccount)
			response, grant := k.issue(t, row.ClientID, created.Credential.Secret)
			if response.StatusCode == 200 {
				identity, err := auth.VerifyWithJWKS(auth.Config{Issuer: k.options.ServerURL + "/realms/workflow", Audience: "gateway-audience", RolesClaim: "hypershell.roles"}, grant["access_token"].(string), keys.Body)
				if err == nil && len(identity.Roles) == 2 {
					t.Error("second Gateway owner obtained a verified admin token for the first Gateway")
				}
			}
			requestJSON(t, "DELETE", path+"/"+created.ID, other, nil)
		}
		if code != 503 {
			t.Fatalf("foreign audience was not refused: %d", code)
		}
		var active int
		if err := f.db.QueryRow("SELECT count(*) FROM service_accounts WHERE gateway_id=$1 AND deleted_at IS NULL", otherGateway.ID).Scan(&active); err != nil || active != 0 {
			t.Fatal("foreign audience left active account metadata")
		}
	})
	parent := t
	for _, failure := range []string{"invalid OIDC", "lost provider binding"} {
		t.Run(failure, func(t *testing.T) {
			k.bindGateway(t, "gateway-audience", gateway.ID)
			path := root + "/" + gateway.ID + "/service_accounts"
			code, body := requestJSON(t, "POST", path, owner, []byte(`{"name":"downgrade-admin","role":"openshell-admin"}`))
			var created struct {
				ID         string `json:"id"`
				Credential struct {
					Secret string `json:"client_secret"`
				} `json:"credential"`
			}
			if code != 201 || json.Unmarshal(body, &created) != nil {
				t.Fatalf("create account: %d", code)
			}
			stored, err := f.storage.Get(context.Background(), "ServiceAccount", created.ID)
			if err != nil {
				t.Fatal(err)
			}
			row := stored.(model.ServiceAccount)
			if failure == "invalid OIDC" {
				code, _ = requestJSON(t, "PATCH", root+"/"+gateway.ID, owner, []byte(`{"oidc":"invalid"}`))
				if code != 200 {
					t.Fatalf("patch OIDC: %d", code)
				}
			} else {
				k.bindGateway(t, "gateway-audience", "")
			}
			if _, err := f.db.Exec("UPDATE role_bindings SET role_id=(SELECT id FROM roles WHERE name='gateway:viewer') WHERE gateway_id=$1 AND user_id=$2", gateway.ID, row.CreatedByUserID); err != nil {
				t.Fatal(err)
			}
			deadline := time.Now().Add(20 * time.Second)
			for {
				response, _ := k.issue(t, row.ClientID, created.Credential.Secret)
				var state string
				if err := f.db.QueryRow("SELECT status FROM service_accounts WHERE id=$1", row.ID).Scan(&state); err != nil {
					t.Fatal(err)
				}
				if response.StatusCode != 200 && state == "revoked" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("invalid Gateway identity preserved admin token issuance after owner downgrade")
				}
				time.Sleep(200 * time.Millisecond)
			}
			// Restore configuration and ownership before restart. Terminal state must remain.
			if _, err := f.db.Exec("UPDATE gateways SET oidc=$1 WHERE id=$2", oidc, gateway.ID); err != nil {
				t.Fatal(err)
			}
			observeGatewayFixture(t, f, gateway.ID)
			if _, err := f.db.Exec("UPDATE role_bindings SET role_id=(SELECT id FROM roles WHERE name='gateway:owner') WHERE gateway_id=$1", gateway.ID); err != nil {
				t.Fatal(err)
			}
			k.bindGateway(t, "gateway-audience", gateway.ID)
			stop()
			stop, address = startApplication(parent, binary, f.dsn, config, settings...)
			root = address + "/api/hypershell/v1/gateways"
			code, body = requestJSON(t, "GET", root+"/"+gateway.ID+"/service_accounts/"+row.ID, owner, nil)
			var result struct {
				Status string `json:"status"`
			}
			if code != 200 || json.Unmarshal(body, &result) != nil || result.Status != "revoked" {
				t.Fatal("restart did not retain revoked metadata")
			}
			response, _ := k.issue(t, row.ClientID, created.Credential.Secret)
			if response.StatusCode == 200 {
				t.Fatal("restored configuration reactivated a revoked credential")
			}
		})
	}
}
