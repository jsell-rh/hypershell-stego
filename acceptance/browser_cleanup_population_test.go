package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
)

const normalCleanupAccounts = 100

type normalCleanupAccount struct{ id, client, provider, subject string }

// This population uses the real browser backend, API, and provider. It remains
// a single Gateway workflow, not a complete installation capacity measurement.
func (w *browserGatewayWorkload) populateNormalCleanup(id string, sample *gatewayCleanupTimingSample) []normalCleanupAccount {
	w.t.Helper()
	ctx, stop := context.WithTimeout(context.Background(), 5*time.Minute)
	defer stop()
	var before int
	if err := w.f.db.QueryRowContext(ctx, "SELECT count(*) FROM service_accounts WHERE gateway_id=$1", id).Scan(&before); err != nil || before != 0 {
		w.t.Fatal("Normal cleanup requires an empty initial account population")
	}
	accounts := make([]normalCleanupAccount, 0, normalCleanupAccounts)
	seen := map[string]bool{}
	for index := range normalCleanupAccounts {
		input, _ := json.Marshal(map[string]string{"name": fmt.Sprintf("complete-cleanup-%03d", index)})
		response := w.owner.apiContext(w.t, ctx, "POST", "/gateways/"+id+"/service_accounts", input)
		if response.StatusCode != http.StatusCreated {
			w.t.Fatal("Normal cleanup account creation failed", index, response.StatusCode)
		}
		var created struct {
			ID         string `json:"id"`
			ClientID   string `json:"client_id"`
			Credential struct {
				Secret string `json:"client_secret"`
			} `json:"credential"`
		}
		err := json.Unmarshal(response.Body, &created)
		clear(response.Body)
		if err != nil || created.ID == "" || created.ClientID == "" || created.Credential.Secret == "" || seen[created.ID] {
			w.t.Fatal("Normal cleanup account response is incomplete or repeated")
		}
		seen[created.ID] = true
		sample.AccountsCreated++
		w.normalCleanupToken(ctx, created.ClientID, created.Credential.Secret)
		created.Credential.Secret = ""
		sample.TokensIssued++
		row := normalCleanupAccount{id: created.ID, client: created.ClientID}
		var ready bool
		err = w.f.db.QueryRowContext(ctx, "SELECT client_uuid,subject,status='ready' AND active AND deleted_at IS NULL AND client_id=$3 FROM service_accounts WHERE id=$1 AND gateway_id=$2", row.id, id, row.client).Scan(&row.provider, &row.subject, &ready)
		if err != nil || !ready || row.provider == "" || row.subject == "" {
			w.t.Fatal("Normal cleanup account did not reach its stored ready state")
		}
		accounts = append(accounts, row)
	}
	w.t.Log("Created 100 normal cleanup accounts through REST and checked token issuance")
	return accounts
}

func (w *browserGatewayWorkload) normalCleanupToken(ctx context.Context, client, secret string) string {
	w.t.Helper()
	form := url.Values{"grant_type": {"client_credentials"}, "client_id": {client}, "client_secret": {secret}}
	response, err := w.identity.http.Do(ctx, http.MethodPost, "/realms/workflow/protocol/openid-connect/token", http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, []byte(form.Encode()))
	if err != nil || response.StatusCode != http.StatusOK {
		w.t.Fatal("Normal cleanup token issuance failed", response.StatusCode)
	}
	var grant struct {
		Token string `json:"access_token"`
	}
	err = json.Unmarshal(response.Body, &grant)
	clear(response.Body)
	if err != nil || grant.Token == "" {
		w.t.Fatal("Normal cleanup token response is incomplete")
	}
	return grant.Token
}

func (w *browserGatewayWorkload) verifyNormalCleanupAccounts(ctx context.Context, id string, accounts []normalCleanupAccount, sample *gatewayCleanupTimingSample) {
	w.t.Helper()
	if len(accounts) != normalCleanupAccounts || sample.AccountsCreated != normalCleanupAccounts || sample.TokensIssued != normalCleanupAccounts {
		w.t.Fatal("Normal cleanup account population is incomplete")
	}
	scope := gateways.AccountProviderStateScope(id)
	membership, err := w.f.storage.LoadResourceStateScope(ctx, "ServiceAccount", scope)
	if err != nil || !membership.Sealed {
		w.t.Fatal("Normal cleanup account scope did not close")
	}
	sample.ScopeSealed = true
	raw, err := os.ReadFile(w.identity.stateKeysFile)
	if err != nil {
		w.t.Fatal("Cannot load the fixture state protector")
	}
	protector, err := runtime.NewStateProtectorFromJSON(raw)
	clear(raw)
	if err != nil {
		w.t.Fatal("Cannot create the fixture state protector")
	}
	admin := w.normalCleanupToken(ctx, "provisioner", "acceptance-only-admin-secret")
	for _, account := range accounts {
		for index, path := range []string{"/clients/" + url.PathEscape(account.provider), "/users/" + url.PathEscape(account.subject)} {
			response, err := w.identity.http.Do(ctx, http.MethodGet, "/admin/realms/workflow"+path, http.Header{"Authorization": {"Bearer " + admin}}, nil)
			if err != nil || response.StatusCode != http.StatusNotFound {
				w.t.Fatal("Normal cleanup left a provider identity or its absence is unproved", response.StatusCode)
			}
			if index == 0 {
				sample.ProviderClientsGone++
			} else {
				sample.ProviderUsersGone++
			}
		}
		sealed, err := w.f.storage.LoadResourceState(ctx, "ServiceAccount", account.id, scope)
		if err != nil {
			w.t.Fatal("Cannot load the normal cleanup account journal")
		}
		state, err := protector.Open(runtime.StateKey{Instance: w.identity.instanceID, Entity: "ServiceAccount", ResourceID: account.id, Scope: scope}, sealed.Version, sealed.Data)
		if err != nil {
			w.t.Fatal("Normal cleanup account journal did not authenticate")
		}
		plain := state.Reveal()
		var journal struct {
			Closed  bool
			Binding struct{ ID, ClientID string }
		}
		err = json.Unmarshal(plain, &journal)
		clear(plain)
		if err != nil || !journal.Closed || journal.Binding.ID != account.provider || journal.Binding.ClientID != account.client {
			w.t.Fatal("Normal cleanup journal did not close its exact provider identity")
		}
		sample.JournalsClosed++
		var closed bool
		var audits int
		err = w.f.db.QueryRowContext(ctx, `SELECT deleted_at IS NOT NULL AND NOT active AND revoked_at IS NOT NULL,
(SELECT count(*) FROM service_account_audits WHERE service_account_id=$1 AND action='gateway_cleanup' AND outcome='succeeded')
FROM service_accounts WHERE id=$1 AND gateway_id=$2`, account.id, id).Scan(&closed, &audits)
		if err != nil || !closed || audits != 1 {
			w.t.Fatal("Normal cleanup did not close an account with one success audit")
		}
		sample.AccountsClosed++
		sample.CleanupAudits += audits
	}
}
