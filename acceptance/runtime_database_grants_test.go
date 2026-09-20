package acceptance

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	databaseaccess "github.com/jsell-rh/hypershell-stego/out/contracts/databaseaccess"
)

// Use the generated object contract for process and Deployment fixtures.
// Only the operator connection can install access for the separate login.
func grantAPIFixtureRuntimeAccess(t testing.TB, f *fixture, role string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	conn, err := f.db.Conn(ctx)
	if err != nil {
		t.Fatal("cannot open the API fixture owner connection")
	}
	defer conn.Close()
	if err := databaseaccess.GrantRuntime(ctx, conn, role); err != nil {
		t.Fatal("cannot install the generated API runtime access contract")
	}
}

func fixtureRuntimeDSN(t testing.TB, source, database, role, password string) string {
	t.Helper()
	// Quote keyword values and verify the selected database and login.
	value := func(s string) string {
		return "'" + strings.ReplaceAll(strings.ReplaceAll(s, `\`, `\\`), "'", `\'`) + "'"
	}
	dsn := source + " dbname=" + value(database) + " user=" + value(role) + " password=" + value(password)
	if strings.HasPrefix(source, "postgres://") || strings.HasPrefix(source, "postgresql://") {
		address, err := url.Parse(source)
		if err != nil {
			t.Fatal("invalid fixture database address")
		}
		address.Path, address.RawPath, address.User = "/"+database, "", url.UserPassword(role, password)
		query := address.Query()
		for _, key := range []string{"dbname", "database", "user", "password"} {
			query.Del(key)
		}
		address.RawQuery = query.Encode()
		dsn = address.String()
	}
	cfg, err := pgx.ParseConfig(dsn)
	if err != nil || cfg.Database != database || cfg.User != role || cfg.Password != password {
		t.Fatal("fixture runtime database settings differ from the selected login")
	}
	return dsn
}
