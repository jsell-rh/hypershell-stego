package acceptance

import (
	"context"
	"encoding/hex"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	browserschema "github.com/jsell-rh/hypershell-stego/gateway-console/out/browser/schema"
)

// The fixture operator installs the generated schema. The browser gets only
// the runtime login. Its database has no API tables or API credentials.
func browserDatabase(t *testing.T) *fixture {
	t.Helper()
	dsn := os.Getenv("STEGO_TEST_POSTGRES_DSN")
	if dsn == "" {
		if os.Getenv("STEGO_REQUIRE_POSTGRES") == "1" {
			t.Fatal("PostgreSQL acceptance tests require STEGO_TEST_POSTGRES_DSN")
		}
		t.Skip("set STEGO_TEST_POSTGRES_DSN for browser acceptance tests")
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil {
		t.Fatal("invalid fixture database settings")
	}
	admin := stdlib.OpenDB(*cfg)
	admin.SetMaxOpenConns(1)
	t.Cleanup(func() { admin.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	suffix := hex.EncodeToString(makeRandom(t, 16))
	name, owner, runtime := "hypershell_test_"+suffix, "console_owner_"+suffix, "console_runtime_"+suffix
	password := hex.EncodeToString(makeRandom(t, 32))
	for _, role := range []struct{ name, options string }{
		{owner, "NOLOGIN NOINHERIT"},
		{runtime, "LOGIN NOINHERIT PASSWORD '" + password + "'"},
	} {
		id := pgx.Identifier{role.name}.Sanitize()
		if _, err := admin.ExecContext(ctx, "CREATE ROLE "+id+" "+role.options); err != nil {
			t.Fatal("cannot create console database role")
		}
		t.Cleanup(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if _, err := admin.ExecContext(ctx, "DROP ROLE "+id); err != nil {
				t.Error("cannot remove console database role", err)
			}
		})
	}
	databaseID, ownerID, runtimeID := pgx.Identifier{name}.Sanitize(), pgx.Identifier{owner}.Sanitize(), pgx.Identifier{runtime}.Sanitize()
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+databaseID+" OWNER "+ownerID); err != nil {
		t.Fatal("cannot create console database", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if _, err := admin.ExecContext(ctx, "DROP DATABASE "+databaseID+" WITH (FORCE)"); err != nil {
			t.Error("cannot remove console database", err)
		}
	})
	cfg.Database = name
	setup := stdlib.OpenDB(*cfg)
	setup.SetMaxOpenConns(1)
	defer setup.Close()
	tx, err := setup.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	schemaSQL, err := os.ReadFile("../console/out/browser/schema.sql")
	if err != nil {
		t.Fatal(err)
	}
	// Both browser modules use the same STEGO session schema. The root
	// already imports the pinned Gateway console module. Require the selected
	// management console SQL to match before using its generated installer.
	if string(schemaSQL) != browserschema.SQL {
		t.Fatal("browser modules selected different session schema contracts")
	}
	for _, statement := range []string{
		"SET LOCAL ROLE " + ownerID,
		"REVOKE ALL ON DATABASE " + databaseID + " FROM PUBLIC",
		"GRANT CONNECT ON DATABASE " + databaseID + " TO " + runtimeID,
		"REVOKE ALL ON SCHEMA public FROM PUBLIC",
		"GRANT USAGE ON SCHEMA public TO " + runtimeID,
	} {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			t.Fatal("cannot install console database schema", err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	ownerConnection, err := setup.Conn(ctx)
	if err != nil {
		t.Fatal("cannot open the browser schema owner connection")
	}
	defer ownerConnection.Close()
	if _, err := ownerConnection.ExecContext(ctx, "SET ROLE "+ownerID); err != nil {
		t.Fatal("cannot select the browser schema owner")
	}
	if err := browserschema.Bootstrap(ctx, ownerConnection, runtime); err != nil {
		t.Fatal("cannot install the generated browser session schema")
	}
	privateDSN := fixtureRuntimeDSN(t, dsn, name, runtime, password)
	limited, err := pgx.ParseConfig(privateDSN)
	if err != nil || limited.Database != name || limited.User != runtime || limited.Password != password {
		t.Fatal("invalid console runtime database settings")
	}
	db := stdlib.OpenDB(*limited)
	db.SetMaxOpenConns(2)
	t.Cleanup(func() { db.Close() })
	if err := browserschema.Verify(ctx, db); err != nil {
		t.Fatal("console runtime access differs from the generated schema contract")
	}
	return &fixture{db: db, runtime: db, dsn: privateDSN}
}
