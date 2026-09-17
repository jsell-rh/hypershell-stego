package acceptance

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"time"
)

// The provisioner supplies console credentials during reconciliation. A ready
// replacement process does not prove that the controllers have recovered.
func (w *browserGatewayWorkload) checkProvisionerRestart(restart func(func())) {
	t := w.t
	t.Helper()
	for _, id := range w.gatewayIDs {
		w.check(id)
	}
	before := w.checkSQLIsolation()
	denied := 0
	restart(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		for _, id := range w.gatewayIDs {
			for {
				var unavailable bool
				err := w.f.db.QueryRowContext(ctx, `SELECT
COALESCE(status='WorkloadUnavailable' AND phase='Degraded',false)
AND COALESCE((stego_observations->>'workload')::bigint,0)=stego_generation
AND deleted_at IS NULL FROM gateways WHERE id=$1`, id).Scan(&unavailable)
				if err != nil {
					t.Fatal("cannot read the Gateway observation during the provisioner outage")
				}
				if unavailable {
					break
				}
				select {
				case <-ctx.Done():
					t.Fatal("the controller did not observe the provisioner outage")
				case <-time.After(time.Second):
				}
			}
			response := w.owner.api(t, "POST", "/gateways/"+id+"/service_accounts", []byte(`{"name":"provisioner-outage-denied","role":"openshell-admin"}`))
			if response.StatusCode != 409 || accountCreationProblem(response.Body) != "gateway_not_ready" {
				t.Fatal("account creation was not denied during the provisioner outage", response.StatusCode)
			}
			var count int
			if err := w.f.db.QueryRowContext(ctx, "SELECT count(*) FROM service_accounts WHERE gateway_id=$1 AND name=$2", id, "provisioner-outage-denied").Scan(&count); err != nil || count != 0 {
				t.Fatal("the denied request created account state")
			}
			denied++
		}
	})
	for _, id := range w.gatewayIDs {
		w.check(id)
	}
	if !reflect.DeepEqual(before, w.checkSQLIsolation()) {
		t.Fatal("provisioner restart changed Gateway SQL or credential identities")
	}
	if denied != 2 {
		t.Fatal("provisioner recovery requires two observed Gateway denials")
	}
	if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
		record := map[string]any{
			"gateways": len(w.gatewayIDs), "denied_account_requests": denied,
			"current_unavailable_observations": true, "denied_requests_created_no_accounts": true,
			"running_healthy_gateways_after_restart": true, "sql_and_credential_identities_preserved": true,
			"account_write_retries": 0,
		}
		data, err := json.MarshalIndent(record, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(directory, "provisioner-restart.json"), append(data, '\n'), 0600) != nil {
			t.Fatal("cannot save provisioner recovery evidence")
		}
	}
	t.Log("Provisioner outage denied new accounts for both Gateways; controllers recovered after restart without account write retries or changes to SQL and credential identities")
}
