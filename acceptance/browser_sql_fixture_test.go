package acceptance

import (
	"os"
	"time"

	"github.com/jackc/pgx/v5"
)

// The test starts its PostgreSQL server before any Gateway controller.
func (w *browserGatewayWorkload) sqlFixtureConfig() *pgx.ConnConfig {
	w.t.Helper()
	if w.sqlFixture != nil {
		return w.sqlFixture.Copy()
	}
	config, err := pgx.ParseConfig(os.Getenv("STEGO_TEST_POSTGRES_DSN"))
	if err != nil || config.TLSConfig == nil || config.TLSConfig.InsecureSkipVerify || config.TLSConfig.RootCAs == nil || len(config.Fallbacks) != 0 || config.Host != "127.0.0.1" {
		w.t.Fatal("SQL fixture requires verified loopback TLS")
	}
	config.ConnectTimeout = 5 * time.Second
	w.sqlFixture = config
	return config.Copy()
}
