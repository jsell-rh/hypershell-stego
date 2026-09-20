package acceptance

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	"github.com/twmb/franz-go/pkg/kgo"
)

type gatewayCleanupTimingSample struct {
	GatewayID            string                             `json:"gateway_id"`
	AccountRows          int64                              `json:"account_rows_before_request"`
	LiveAccountRows      int64                              `json:"undeleted_account_rows_before_request"`
	AcceptedAt           string                             `json:"accepted_response_observed_at"`
	Complete             bool                               `json:"complete"`
	Seconds              float64                            `json:"observed_upper_bound_seconds"`
	WithinTargetObserved bool                               `json:"within_target_observed"`
	AccountsCreated      int                                `json:"accounts_created_through_rest"`
	TokensIssued         int                                `json:"accounts_with_verified_token_issuance"`
	AccountsClosed       int                                `json:"accounts_closed"`
	JournalsClosed       int                                `json:"journals_closed"`
	ProviderClientsGone  int                                `json:"provider_clients_absent"`
	ProviderUsersGone    int                                `json:"provider_users_absent"`
	CleanupAudits        int                                `json:"cleanup_success_audits"`
	ScopeSealed          bool                               `json:"account_scope_sealed"`
	Observations         map[string]cleanupStageObservation `json:"completion_observations,omitempty"`
}

type gatewayCleanupTimingRecord struct {
	Schema                    int                          `json:"schema"`
	Scope                     string                       `json:"scope"`
	Stage                     string                       `json:"stage"`
	Observation               string                       `json:"observation"`
	TargetSeconds             int                          `json:"target_seconds"`
	CapacityFixture           bool                         `json:"capacity_fixture"`
	AccountTarget             int                          `json:"live_accounts_per_measured_gateway"`
	Complete                  bool                         `json:"complete"`
	InstallationDataPreserved bool                         `json:"installation_data_preserved"`
	Gateways                  []gatewayCleanupTimingSample `json:"gateways"`
}

// Normal deletion must finish before test fixture cleanup runs.
func (w *browserGatewayWorkload) checkSuppliedDatabaseRetention(operator *consoleBrowser, consumer *kgo.Client) {
	w.t.Helper()
	// This phase has no deliberate cleanup denial. The earlier fault case is
	// separate. Counts are sampled before each request, outside its transaction.
	record := gatewayCleanupTimingRecord{Schema: 2, Scope: "normal_gateway_cleanup", Stage: "population", TargetSeconds: 30, AccountTarget: normalCleanupAccounts, Observation: "sequential_completion_checks"}
	accepted := map[string]time.Time{}
	indexes := map[string]int{}
	populations := map[string][]normalCleanupAccount{}
	w.t.Cleanup(func() {
		if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
			data, err := json.MarshalIndent(record, "", "  ")
			if err != nil || os.WriteFile(filepath.Join(directory, "gateway-cleanup-timing.json"), append(data, '\n'), 0600) != nil {
				w.t.Error("Cannot save Gateway cleanup timing")
			}
		}
	})
	if response := operator.api(w.t, "DELETE", "/managed_clusters/"+w.f.cluster, nil); response.StatusCode != 409 {
		w.t.Fatal("cluster deletion did not protect the remaining Gateway", response.StatusCode)
	}
	for _, id := range w.gatewayIDs {
		response := w.owner.api(w.t, "GET", "/gateways/"+id, nil)
		if response.StatusCode == 404 {
			continue
		}
		if response.StatusCode != 200 {
			w.t.Fatal("remaining Gateway read failed", response.StatusCode)
		}
		if _, duplicate := indexes[id]; duplicate {
			w.t.Fatal("Duplicate Gateway cleanup sample")
		}
		indexes[id] = len(record.Gateways)
		record.Gateways = append(record.Gateways, gatewayCleanupTimingSample{GatewayID: id})
		sample := &record.Gateways[indexes[id]]
		populations[id] = w.populateNormalCleanup(id, sample)
		var deleting bool
		read, stop := context.WithTimeout(context.Background(), 5*time.Second)
		err := w.f.db.QueryRowContext(read, "SELECT g.deleted_at IS NOT NULL,count(a.id),count(a.id) FILTER (WHERE a.deleted_at IS NULL) FROM gateways g LEFT JOIN service_accounts a ON a.gateway_id=g.id WHERE g.id=$1 GROUP BY g.deleted_at", id).Scan(&deleting, &sample.AccountRows, &sample.LiveAccountRows)
		stop()
		if err != nil || deleting || sample.AccountRows != normalCleanupAccounts || sample.LiveAccountRows != normalCleanupAccounts {
			w.t.Fatal("Cannot read the Gateway cleanup population", err)
		}
	}
	record.Stage = "requests"
	// Prepare every population before any measured deletion starts.
	for _, sample := range record.Gateways {
		id := sample.GatewayID
		response := w.owner.api(w.t, "DELETE", "/gateways/"+id, nil)
		if response.StatusCode != 202 {
			w.t.Fatal("remaining Gateway deletion failed", response.StatusCode)
		}
		accepted[id] = time.Now()
		record.Gateways[indexes[id]].AcceptedAt = accepted[id].UTC().Format(time.RFC3339Nano)
	}
	if len(accepted) == 0 {
		w.t.Fatal("No normal Gateway deletion was accepted")
	}
	record.Stage = "cleanup"
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	allocator, err := allocation.New(w.kubernetes, w.p.namespace)
	if err != nil {
		w.t.Fatal(err)
	}
	for _, id := range w.gatewayIDs {
		observe := func(string, bool) {}
		if started, measured := accepted[id]; measured {
			sample := &record.Gateways[indexes[id]]
			observe = func(stage string, complete bool) {
				sample.observeCleanup(stage, complete, time.Since(started))
			}
		}
		w.awaitGatewayCleanupObserved(ctx, allocator, id, observe)
		w.requireSQLAbsent(ctx, w.databaseOptions, id)
		observe("sql_objects_absent", true)
		if response := w.owner.api(w.t, "GET", "/gateways/"+id, nil); response.StatusCode != 404 {
			w.t.Fatal("Finalized Gateway remained readable", response.StatusCode)
		}
		observe("owner_http_404", true)
		if started, ok := accepted[id]; ok {
			sample := &record.Gateways[indexes[id]]
			w.verifyNormalCleanupAccounts(ctx, id, populations[id], sample)
			observe("account_proof_complete", true)
			elapsed := time.Since(started)
			// Sequential checks can observe completion after it occurred. This is
			// an upper bound, not proof of a missed target when it exceeds 30 seconds.
			sample.Complete, sample.Seconds, sample.WithinTargetObserved = true, elapsed.Seconds(), elapsed <= 30*time.Second
		}
	}
	record.Stage = "preservation"
	w.requireInstallationData(ctx)
	record.InstallationDataPreserved = true
	if response := operator.api(w.t, "DELETE", "/managed_clusters/"+w.f.cluster, nil); response.StatusCode != 204 {
		w.t.Fatal("finished Gateway cleanup did not release its cluster", response.StatusCode)
	}
	readCatalogEvent(w.t, consumer, w.f.cluster, "ManagedClusters", "Delete", "managedcluster.deleted")
	record.Stage, record.Complete = "complete", true
	w.t.Log("Browser deletion removed all Gateway SQL and state; the supplied PostgreSQL server and installation data remain")
}

func (w *browserGatewayWorkload) awaitGatewayCleanup(ctx context.Context, allocator *allocation.Allocator, id string) {
	w.t.Helper()
	w.awaitGatewayCleanupObserved(ctx, allocator, id, func(string, bool) {})
}

func (w *browserGatewayWorkload) awaitGatewayCleanupObserved(ctx context.Context, allocator *allocation.Allocator, id string, observe func(string, bool)) {
	w.t.Helper()
	ns, err := gatewayworkload.Namespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	state, err := gatewayworkload.StateNamespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	consoleState, err := gatewayworkload.ConsoleStateNamespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	sandbox, err := gatewayworkload.SandboxNamespace(id)
	if err != nil {
		w.t.Fatal(err)
	}
	for {
		gone, err := allocator.NamespaceGone(ctx, "gateway", ns, id)
		if err != nil {
			w.t.Fatal("Gateway namespace read failed", err)
		}
		observe("gateway_namespace_absent", gone)
		stateGone, err := allocator.NamespaceGone(ctx, "gateway-state", state, id)
		if err != nil {
			w.t.Fatal("Gateway state namespace read failed", err)
		}
		observe("gateway_state_namespace_absent", stateGone)
		consoleGone, err := allocator.NamespaceGone(ctx, "gateway-console-state", consoleState, id)
		if err != nil {
			w.t.Fatal("Gateway console state namespace read failed", err)
		}
		observe("console_state_namespace_absent", consoleGone)
		sandboxGone, err := allocator.NamespaceGone(ctx, "sandbox", sandbox, id)
		if err != nil {
			w.t.Fatal("Sandbox namespace read failed", err)
		}
		observe("sandbox_namespace_absent", sandboxGone)
		var complete, accounts, identity, workload, sql, finalized bool
		err = w.f.db.QueryRowContext(ctx, `SELECT
COALESCE(deleted_at IS NOT NULL AND stego_finalized_at IS NOT NULL AND stego_cleanup->>'accounts'='true' AND stego_cleanup->>'identity'='true' AND stego_cleanup_targets->'workload'->>$2='true' AND stego_cleanup_targets->'sql'->>$2='true',false),
COALESCE(stego_cleanup->>'accounts'='true',false),
COALESCE(stego_cleanup->>'identity'='true',false),
COALESCE(stego_cleanup_targets->'workload'->>$2='true',false),
COALESCE(stego_cleanup_targets->'sql'->>$2='true',false),
stego_finalized_at IS NOT NULL
FROM gateways WHERE id=$1`, id, w.f.cluster).Scan(&complete, &accounts, &identity, &workload, &sql, &finalized)
		if err != nil {
			w.t.Fatal("Gateway cleanup read failed", err)
		}
		observe("account_cleanup_recorded", accounts)
		observe("identity_cleanup_recorded", identity)
		observe("workload_cleanup_recorded", workload)
		observe("sql_cleanup_recorded", sql)
		observe("gateway_finalized", finalized)
		observe("all_cleanup_recorded", complete)
		if gone && sandboxGone && stateGone && consoleGone && complete {
			break
		}
		select {
		case <-ctx.Done():
			w.t.Fatal("Gateway SQL and workload cleanup did not finish")
		case <-time.After(time.Second):
		}
	}
	for _, profile := range []string{"gateway", "sandbox", "gateway-state", "gateway-console-state"} {
		w.checkNoAllocationBindings(ctx, allocator, profile, id)
	}
	observe("allocation_bindings_absent", true)
}

func (w *browserGatewayWorkload) checkNoAllocationBindings(ctx context.Context, allocator *allocation.Allocator, profile, id string) {
	w.t.Helper()
	selector := allocation.MarkerLabel + "=" + allocator.Marker() + "," + allocation.ProfileLabel + "=" + profile + ",hypershell.redhat.io/gateway-id=" + id
	bindings, code, err := w.kubernetes.Request(ctx, "GET", "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings?limit=64&labelSelector="+url.QueryEscape(selector), nil)
	entries, ok := kube.Nested(bindings, "items").([]any)
	if err != nil || code != 200 || !ok || len(entries) != 0 || kube.String(bindings, "metadata", "continue") != "" {
		w.t.Fatal("allocation cluster bindings remain after cleanup", profile, code)
	}
}
