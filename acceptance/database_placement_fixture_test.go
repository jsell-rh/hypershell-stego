package acceptance

import (
	"fmt"
	"github.com/segmentio/ksuid"
	"testing"
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
