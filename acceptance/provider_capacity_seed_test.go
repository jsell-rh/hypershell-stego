package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	web "github.com/jsell-rh/hypershell-stego/out/application/client"
)

type capacityBackgroundAccount struct{ gateway, account, client, subject, name string }

// Each API request is a separate provider transaction. A single realm import
// kept all 9,900 clients and users in one transaction and exceeded eight minutes.
func seedCapacityAccounts(t *testing.T, parent context.Context, k *keycloakFixture, rows []capacityBackgroundAccount) {
	t.Helper()
	ctx, cancel := context.WithTimeout(parent, 8*time.Minute)
	defer cancel()
	var tokenMu sync.Mutex
	var token string
	var refresh time.Time
	authorization := func(ctx context.Context) (string, error) {
		tokenMu.Lock()
		defer tokenMu.Unlock()
		if time.Now().Before(refresh) {
			return token, nil
		}
		form := url.Values{"grant_type": {"client_credentials"}, "client_id": {"provisioner"}, "client_secret": {"acceptance-only-admin-secret"}}
		response, err := k.http.Do(ctx, http.MethodPost, "/realms/workflow/protocol/openid-connect/token", http.Header{"Content-Type": {"application/x-www-form-urlencoded"}}, []byte(form.Encode()))
		var grant struct {
			AccessToken string `json:"access_token"`
		}
		if err != nil || response.StatusCode != http.StatusOK || json.Unmarshal(response.Body, &grant) != nil || grant.AccessToken == "" {
			return "", errors.New("capacity seed administrator login failed")
		}
		token, refresh = grant.AccessToken, time.Now().Add(time.Minute)
		return token, nil
	}
	request := func(method, path string, value any, expected int) (web.Response, error) {
		call, done := context.WithTimeout(ctx, 10*time.Second)
		defer done()
		bearer, err := authorization(call)
		if err != nil {
			return web.Response{}, err
		}
		var body []byte
		if value != nil {
			body, err = json.Marshal(value)
			if err != nil {
				return web.Response{}, err
			}
		}
		response, err := k.http.Do(call, method, "/admin/realms/workflow"+path, http.Header{"Authorization": {"Bearer " + bearer}, "Content-Type": {"application/json"}}, body)
		if err != nil {
			return response, err
		}
		if response.StatusCode != expected {
			return response, fmt.Errorf("capacity seed request %s returned %d; expected %d", method, response.StatusCode, expected)
		}
		return response, nil
	}
	type roleBinding struct {
		client string
		role   json.RawMessage
	}
	roles := map[string]roleBinding{}
	for _, row := range rows {
		if _, exists := roles[row.gateway]; exists {
			continue
		}
		name, err := keycloak.GatewayClientID(row.gateway)
		if err != nil {
			t.Fatal(err)
		}
		response, err := request(http.MethodGet, "/clients?clientId="+url.QueryEscape(name), nil, http.StatusOK)
		var clients []struct {
			ID string `json:"id"`
		}
		if err != nil || json.Unmarshal(response.Body, &clients) != nil || len(clients) != 1 || clients[0].ID == "" {
			t.Fatal("capacity Gateway role client is absent", err)
		}
		response, err = request(http.MethodGet, "/clients/"+clients[0].ID+"/roles/openshell-user", nil, http.StatusOK)
		if err != nil {
			t.Fatal(err)
		}
		roles[row.gateway] = roleBinding{clients[0].ID, response.Body}
	}
	seed := func(index int) error {
		row := &rows[index]
		response, err := request(http.MethodPost, "/clients", map[string]any{"clientId": row.name, "enabled": true, "protocol": "openid-connect", "secret": "capacity-fixture-only", "publicClient": false, "serviceAccountsEnabled": true, "standardFlowEnabled": false, "directAccessGrantsEnabled": false, "fullScopeAllowed": false, "defaultClientScopes": []string{}, "optionalClientScopes": []string{}, "attributes": map[string]string{"stego.owner.hypershell.service-account": "true", "stego.owner.hypershell.gateway-id": row.gateway, "stego.owner.hypershell.service-account-id": row.account}}, http.StatusCreated)
		if err != nil {
			return err
		}
		location, err := url.Parse(response.Header.Get("Location"))
		if err != nil {
			return errors.New("invalid capacity client location")
		}
		id, ok := strings.CutPrefix(location.Path, "/admin/realms/workflow/clients/")
		if !ok || id == "" || strings.Contains(id, "/") {
			return errors.New("invalid capacity provider client ID")
		}
		row.client = id
		response, err = request(http.MethodGet, "/clients/"+id+"/service-account-user", nil, http.StatusOK)
		var user struct {
			ID      string `json:"id"`
			Enabled bool   `json:"enabled"`
		}
		if err != nil || json.Unmarshal(response.Body, &user) != nil || user.ID == "" || !user.Enabled {
			return errors.New("capacity provider service user is absent or disabled")
		}
		row.subject = user.ID
		binding := roles[row.gateway]
		_, err = request(http.MethodPost, "/users/"+user.ID+"/role-mappings/clients/"+binding.client, []json.RawMessage{binding.role}, http.StatusNoContent)
		return err
	}
	jobs := make(chan int)
	failures := make(chan error, 1)
	var workers sync.WaitGroup
	var completed atomic.Int64
	for range 4 {
		workers.Go(func() {
			for index := range jobs {
				if ctx.Err() != nil {
					return
				}
				if err := seed(index); err != nil {
					select {
					case failures <- err:
					default:
					}
					cancel()
					return
				}
				if count := completed.Add(1); count%1000 == 0 {
					t.Logf("Seeded %d background provider accounts", count)
				}
			}
		})
	}
queue:
	for index := range rows {
		select {
		case jobs <- index:
		case <-ctx.Done():
			break queue
		}
	}
	close(jobs)
	workers.Wait()
	select {
	case err := <-failures:
		t.Fatal("capacity provider seed failed", err)
	default:
	}
	if ctx.Err() != nil || completed.Load() != int64(len(rows)) {
		t.Fatal("capacity provider seed did not finish", completed.Load(), ctx.Err())
	}
	t.Logf("Seeded all %d background clients, users, and Gateway role grants", len(rows))
}
