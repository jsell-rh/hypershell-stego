package acceptance

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// Keep the API fixture grants equal for process and Deployment tests.
// The runtime can read the schema marker but cannot change it or apply DDL.
func grantAPIFixtureRuntimeAccess(t *testing.T, f *fixture, database, role string) {
	t.Helper()
	identifier := pgx.Identifier{role}.Sanitize()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, statement := range []string{
		"GRANT CONNECT ON DATABASE " + pgx.Identifier{database}.Sanitize() + " TO " + identifier,
		"GRANT USAGE ON SCHEMA public, stego_outbox, stego_schema TO " + identifier,
		"GRANT SELECT ON stego_schema.generation TO " + identifier,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public, stego_outbox TO " + identifier,
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public, stego_outbox TO " + identifier,
	} {
		if _, err := f.db.ExecContext(ctx, statement); err != nil {
			t.Fatal("cannot grant API fixture runtime access", err)
		}
	}
	var canInspect, canChange bool
	if err := f.db.QueryRowContext(ctx, `SELECT
		has_schema_privilege($1,'stego_schema','USAGE') AND has_table_privilege($1,'stego_schema.generation','SELECT'),
		has_schema_privilege($1,'public','CREATE') OR has_schema_privilege($1,'stego_outbox','CREATE') OR
		has_schema_privilege($1,'stego_schema','CREATE') OR
		has_table_privilege($1,'stego_schema.generation','INSERT,UPDATE,DELETE,TRUNCATE,REFERENCES,TRIGGER')`, role).Scan(&canInspect, &canChange); err != nil || !canInspect || canChange {
		t.Fatal("API runtime role must read the schema marker without DDL or marker write access", err)
	}
}
