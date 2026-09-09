package acceptance

import (
	"encoding/json"
	"testing"
)

func gatewayAccountWorkflow(t *testing.T, k *keycloakFixture, address, gatewayID, owner string, call gatewayCall) func() {
	t.Helper()
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
		code, data := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways/"+gatewayID+"/service_accounts", owner, input)
		if code != 201 {
			t.Fatal("Gateway automation identity creation failed", code, string(data))
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
		if !ok {
			t.Fatal("Gateway automation token is absent")
		}
		if _, err := call("GetProvider", token, `{"name":"stored-provider"}`); err != nil {
			t.Fatal("automation identity cannot read its Gateway provider", err)
		}
		accounts = append(accounts, row)
	}
	t.Log("Three service-account identities used the actual Gateway")
	return func() {
		t.Helper()
		for _, row := range accounts {
			response, _ := k.issue(t, row.ClientID, row.Credential.Secret)
			if response.StatusCode != 401 {
				t.Fatal("deleted Gateway still issued automation credentials", response.StatusCode)
			}
			response = k.adminRequest(t, "GET", "/clients?clientId="+row.ClientID, nil)
			var clients []any
			if json.Unmarshal(response.Body, &clients) != nil || len(clients) != 0 {
				t.Fatal("Gateway deletion left an automation client")
			}
		}
		t.Log("Gateway deletion removed all three automation clients before workload teardown")
	}
}
