package acceptance

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
)

func (w *browserGatewayWorkload) prepareEarlyDeletion(owner *consoleBrowser) string {
	w.t.Helper()
	if len(w.stops) != 0 {
		w.t.Fatal("early deletion requires workers that have not started")
	}
	input, _ := json.Marshal(w.f.request("deleted-before-worker-start"))
	response := owner.api(w.t, "POST", "/gateways", input)
	var row httpapi.Gateway
	if response.StatusCode != 201 || json.Unmarshal(response.Body, &row) != nil || row.ID == "" {
		w.t.Fatal("early Gateway creation failed", response.StatusCode)
	}
	if response = owner.api(w.t, "DELETE", "/gateways/"+row.ID, nil); response.StatusCode != 204 {
		w.t.Fatal("early Gateway deletion failed", response.StatusCode)
	}
	var bindings int
	if err := w.f.db.QueryRow("SELECT count(*) FROM stego_effect_bindings WHERE entity='Gateway' AND resource_id=$1", row.ID).Scan(&bindings); err != nil || bindings != 0 {
		w.t.Fatal("SQL state existed before worker startup", err)
	}
	return row.ID
}

func (w *browserGatewayWorkload) checkEarlyDeletion(id string) {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	allocator, err := allocation.New(w.kubernetes, w.p.namespace)
	if err != nil {
		w.t.Fatal(err)
	}
	w.awaitGatewayCleanup(ctx, allocator, id)
	w.requireSQLAbsent(ctx, w.databaseOptions, id)
	var digest string
	var closed bool
	err = w.f.db.QueryRowContext(ctx, "SELECT digest,closed FROM stego_effect_bindings WHERE entity='Gateway' AND resource_id=$1 AND scope=$2", id, "sql-state:"+w.f.cluster).Scan(&digest, &closed)
	if err != nil || !closed || digest != "" {
		w.t.Fatal("early deletion did not retain a closed empty SQL binding", err)
	}
	w.requireInstallationData(ctx)
	w.t.Log("Gateway deletion before worker startup survived API restart and closed SQL registration without creating SQL or namespaces")
}
