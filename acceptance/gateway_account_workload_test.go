package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for index, name := range []string{"gateway-automation-one", "gateway-automation-two", "gateway-automation-three"} {
		input, _ := json.Marshal(map[string]string{"name": name, "role": "openshell-admin"})
		response := w.owner.api(t, "POST", "/gateways/"+gatewayID+"/service_accounts", input)
		code, data := response.StatusCode, response.Body
		if code != 201 {
			w.accountCreationFailure(gatewayID, index+1, code, data)
			t.Fatal("Gateway automation identity creation failed", code, accountCreationProblem(data))
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
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for {
			var complete bool
			if err := w.f.db.QueryRowContext(ctx, "SELECT COALESCE(stego_cleanup->>'accounts'='true',false) FROM gateways WHERE id=$1", gatewayID).Scan(&complete); err != nil {
				t.Fatal("account cleanup observation", err)
			}
			if complete {
				break
			}
			select {
			case <-ctx.Done():
				t.Fatal("account cleanup did not finish")
			case <-time.After(100 * time.Millisecond):
			}
		}
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
			record := map[string]any{"gateway_id": gatewayID, "account_ids": ids, "accounts_used_actual_gateway": len(ids), "token_issuance_denied": len(ids), "provider_clients_absent": len(ids), "metadata_closed": len(ids), "cleanup_audits": len(ids), "checked_after_durable_account_cleanup": true}
			data, err := json.MarshalIndent(record, "", "  ")
			if err != nil || os.WriteFile(filepath.Join(directory, "gateway-account-deletion.json"), append(data, '\n'), 0600) != nil {
				t.Fatal("cannot write Gateway account deletion evidence")
			}
		}
		t.Log("Gateway deletion closed three accounts and their cleanup audits; token issuance was denied and all three provider clients were absent after durable account cleanup")
	}
}

// Only fixed API error codes can enter diagnostics. A response can contain a
// credential or an upstream error, so do not record its body or reason field.
func accountCreationProblem(data []byte) string {
	var problem struct {
		Code string `json:"code"`
	}
	if len(data) > 4096 || json.Unmarshal(data, &problem) != nil {
		return "unrecognized_response"
	}
	switch problem.Code {
	case "gateway_not_ready", "operation_pending", "service_account_name_exists",
		"gateway_quota_exceeded", "creator_quota_exceeded", "keycloak_unavailable",
		"not_found", "role_not_allowed", "internal_error", "unauthorized",
		"invalid_request", "service_unavailable":
		return problem.Code
	default:
		return "unrecognized_response"
	}
}

func (w *browserGatewayWorkload) accountCreationFailure(gatewayID string, attempt, status int, body []byte) {
	t := w.t
	t.Helper()
	record := struct {
		Attempt            int    `json:"attempt"`
		Status             int    `json:"status"`
		Problem            string `json:"problem"`
		StateRead          bool   `json:"state_read"`
		Generation         int64  `json:"generation"`
		WorkloadGeneration int64  `json:"workload_generation"`
		Healthy            bool   `json:"healthy"`
		Running            bool   `json:"running"`
		Deleting           bool   `json:"deleting"`
	}{Attempt: attempt, Status: status, Problem: accountCreationProblem(body)}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err := w.f.db.QueryRowContext(ctx, `SELECT stego_generation,
COALESCE((stego_observations->>'workload')::bigint,0),
COALESCE(status='Healthy',false),COALESCE(phase='Running',false),deleted_at IS NOT NULL
FROM gateways WHERE id=$1`, gatewayID).Scan(&record.Generation, &record.WorkloadGeneration, &record.Healthy, &record.Running, &record.Deleting)
	record.StateRead = err == nil
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		t.Log("Cannot encode account creation failure state")
		return
	}
	t.Logf("Gateway account creation failure state: %s", data)
	if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
		if err := os.WriteFile(filepath.Join(directory, "gateway-account-creation-failure.json"), append(data, '\n'), 0600); err != nil {
			t.Log("Cannot save account creation failure state")
		}
	}
}

func TestAccountCreationProblemExcludesPrivateResponseData(t *testing.T) {
	for _, input := range []string{
		`{"code":"private-credential","reason":"private-credential"}`,
		`{"credential":{"client_secret":"private-credential"}}`,
		`{"code":"gateway_not_ready"} trailing`,
		`{"code":"gateway_not_ready","reason":"` + strings.Repeat("x", 4096) + `"}`,
	} {
		if got := accountCreationProblem([]byte(input)); got != "unrecognized_response" {
			t.Fatal("private or invalid response was accepted")
		}
	}
	for _, code := range []string{"gateway_not_ready", "operation_pending", "service_account_name_exists"} {
		input := `{"code":"` + code + `","reason":"private-credential"}`
		if got := accountCreationProblem([]byte(input)); got != code {
			t.Fatal("fixed error code was not retained")
		}
	}
}
