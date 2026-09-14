package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

// Old catalog transport tests create databases directly. Give only those test
// records an explicit cluster before insertion, so revision assertions remain
// meaningful. Real Gateway placement tests do not install this fixture.
func assignTestDatabaseCluster(t *testing.T, f *fixture) {
	t.Helper()
	id, err := ksuid.Parse(f.cluster)
	if err != nil || id == ksuid.Nil || id.String() != f.cluster {
		t.Fatal("invalid test cluster")
	}
	sql := fmt.Sprintf(`CREATE FUNCTION acceptance_database_placement() RETURNS trigger LANGUAGE plpgsql AS $$
 BEGIN IF NEW.provider='deployment' AND NEW.cluster_id IS NULL THEN NEW.cluster_id := TG_ARGV[0]; END IF; RETURN NEW; END $$;
 CREATE TRIGGER acceptance_database_placement BEFORE INSERT ON managed_databases FOR EACH ROW EXECUTE FUNCTION acceptance_database_placement('%s')`, f.cluster)
	if _, err := f.db.Exec(sql); err != nil {
		t.Fatal(err)
	}
}

// Catalog tests register servers through the API. Seed only their cluster.
func databaseCatalogFixture(t *testing.T) *fixture {
	t.Helper()
	f := databaseSetup(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := f.storage.Create(ctx, "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: f.cluster}, Name: "catalog-cluster", Provider: "kubernetes", KubeconfigSecret: "test-cluster"}); err != nil {
		t.Fatal(err)
	}
	return f
}

func databaseCreateBody(t *testing.T, name, provider, cluster string) []byte {
	t.Helper()
	body, err := json.Marshal(catalog.DatabaseCreate{Name: name, Provider: provider, ClusterID: cluster})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

// restoreLegacyGatewayPlacement models an old move with a shared database.
// Stop the API before this fixture runs. Current constraints are restored in
// the same transaction; production requests cannot use this path.
func restoreLegacyGatewayPlacement(t *testing.T, f *fixture, gateway, cluster string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	tx, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `ALTER TABLE managed_databases DROP CONSTRAINT hypershell_database_locality`); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE managed_databases SET cluster_id=NULL WHERE id=(SELECT database_id FROM gateways WHERE id=$1)`, gateway); err != nil {
		t.Fatal(err)
	}
	result, err := tx.ExecContext(ctx, `UPDATE gateways SET cluster_id=$1 WHERE id=$2`, cluster, gateway)
	if err != nil {
		t.Fatal(err)
	}
	if n, err := result.RowsAffected(); err != nil || n != 1 {
		t.Fatal("legacy placement requires one Gateway", n, err)
	}
	if _, err := tx.ExecContext(ctx, `ALTER TABLE managed_databases ADD CONSTRAINT hypershell_database_locality CHECK (cluster_id IS NOT NULL) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}
