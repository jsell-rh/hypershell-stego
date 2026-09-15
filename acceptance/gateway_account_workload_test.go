package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"google.golang.org/grpc/status"
)

func (w *browserGatewayWorkload) startAccountDeletionWorkflow(gatewayID string) func() {
	t := w.t
	t.Helper()
	k, call := w.identity, w.call
	type account struct {
		ID         string `json:"id"`
		ClientID   string `json:"client_id"`
		Credential struct {
			Secret string `json:"client_secret"`
		} `json:"credential"`
	}
	accounts := []account{}
	for _, name := range []string{"gateway-automation-one", "gateway-automation-two", "gateway-automation-three"} {
		input, _ := json.Marshal(map[string]string{"name": name, "role": "openshell-admin"})
		response := w.owner.api(t, "POST", "/gateways/"+gatewayID+"/service_accounts", input)
		code, data := response.StatusCode, response.Body
		if code != 201 {
			t.Fatal("Gateway automation identity creation failed", code)
		}
		var row account
		if json.Unmarshal(data, &row) != nil || row.ID == "" || row.ClientID == "" || row.Credential.Secret == "" {
			t.Fatal("Gateway automation identity response is incomplete")
		}
		response, grant := k.issue(t, row.ClientID, row.Credential.Secret)
		if response.StatusCode != 200 {
			t.Fatal("Gateway automation token issuance failed", response.StatusCode)
		}
		token, ok := grant["access_token"].(string)
		if !ok || token == "" {
			t.Fatal("Gateway automation token is absent")
		}
		if _, err := call("GetProvider", token, `{"name":"browser-provider"}`); err != nil {
			t.Fatal("automation identity cannot read its Gateway provider", status.Code(err))
		}
		accounts = append(accounts, row)
	}
	t.Log("Three service-account identities used the actual Gateway")
	return func() {
		t.Helper()
		ids := make([]string, 0, len(accounts))
		for _, row := range accounts {
			ids = append(ids, row.ID)
			response, _ := k.issue(t, row.ClientID, row.Credential.Secret)
			if response.StatusCode != 401 {
				t.Fatal("deleted Gateway still issued automation credentials", response.StatusCode)
			}
			response = k.adminRequest(t, "GET", "/clients?clientId="+row.ClientID, nil)
			var clients []any
			if json.Unmarshal(response.Body, &clients) != nil || len(clients) != 0 {
				t.Fatal("Gateway deletion left an automation client")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			var closed bool
			var audits int
			err := w.f.db.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL AND NOT active AND revoked_at IS NOT NULL,
(SELECT count(*) FROM service_account_audits WHERE service_account_id=$1 AND action='gateway_cleanup' AND outcome='succeeded')
FROM service_accounts WHERE id=$1 AND gateway_id=$2`, row.ID, gatewayID).Scan(&closed, &audits)
			cancel()
			if err != nil || !closed || audits != 1 {
				t.Fatal("Gateway deletion did not commit account closure and one cleanup audit")
			}
		}
		if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
			record := map[string]any{"gateway_id": gatewayID, "account_ids": ids, "accounts_used_actual_gateway": len(ids), "token_issuance_denied": len(ids), "provider_clients_absent": len(ids), "metadata_closed": len(ids), "cleanup_audits": len(ids), "checked_after_delete_response": true}
			data, err := json.MarshalIndent(record, "", "  ")
			if err != nil || os.WriteFile(filepath.Join(directory, "gateway-account-deletion.json"), append(data, '\n'), 0600) != nil {
				t.Fatal("cannot write Gateway account deletion evidence")
			}
		}
		t.Log("Gateway deletion closed three accounts and their cleanup audits; token issuance was denied and all three provider clients were absent after the API response")
	}
}
