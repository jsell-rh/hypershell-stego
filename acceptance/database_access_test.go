package acceptance

import (
	"context"
	"encoding/json"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	databaseaccess "github.com/jsell-rh/hypershell-stego/out/contracts/databaseaccess"
)

func TestFixtureRuntimeDSN(t *testing.T) {
	for _, source := range []string{
		"postgres://old:old@127.0.0.1:5434/old?user=override&password=override&dbname=override&sslmode=disable&application_name=access-fixture",
		"host=127.0.0.1 port=5434 dbname=old user=old password=old sslmode=disable application_name=access-fixture",
	} {
		dsn := fixtureRuntimeDSN(t, source, "fixture_database", "fixture_runtime", `test-'\-password`)
		cfg, err := pgx.ParseConfig(dsn)
		if err != nil || cfg.Host != "127.0.0.1" || cfg.Port != 5434 || cfg.TLSConfig != nil || cfg.RuntimeParams["application_name"] != "access-fixture" {
			t.Fatal("runtime credentials changed fixture connection options")
		}
	}
}

// A valid schema is not sufficient. The generated application must reject
// incomplete or excessive runtime access and keep data through owner repair.
func TestGatewayDatabaseAccessStartupAndRepair(t *testing.T) {
	f := database(t)
	cfg, err := pgx.ParseConfig(f.dsn)
	if err != nil {
		t.Fatal("invalid runtime fixture configuration")
	}
	_, brokerConfig := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, brokerConfig)
	key, settings := issuer(t)
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, brokerConfig, settings...)
	defer func() { stop() }()
	owner := token(t, key, "alice", "gateway:creator")
	denied := token(t, key, "mallory")
	input, err := json.Marshal(f.request("access-repair"))
	if err != nil {
		t.Fatal(err)
	}
	code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, input)
	var row struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(body, &row) != nil || row.ID == "" {
		t.Fatal("cannot create Gateway before access fault", code)
	}
	readEvent(t, consumer, row.ID)
	awaitQueueEmpty(t, f)
	stop()
	role := pgx.Identifier{cfg.User}.Sanitize()
	for _, mode := range []struct{ name, fault, repair string }{
		{"missing-marker-read", "REVOKE SELECT ON stego_schema.generation FROM " + role, ""},
		{"excess-table-access", "GRANT TRUNCATE ON public.gateways TO " + role, "REVOKE TRUNCATE ON public.gateways FROM " + role},
	} {
		t.Run(mode.name, func(t *testing.T) {
			execute := func(statement string) {
				t.Helper()
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if _, err := f.db.ExecContext(ctx, statement); err != nil {
					t.Fatal("cannot change fixture access")
				}
			}
			execute(mode.fault)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			command := exec.CommandContext(ctx, binary)
			command.Env = applicationEnvironment(t, f.dsn, brokerConfig, settings...)
			output, err := command.CombinedOutput()
			deadline := ctx.Err()
			cancel()
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || deadline != nil {
				t.Fatal("unsafe runtime access did not stop application startup")
			}
			var record map[string]any
			if json.Unmarshal(output, &record) != nil || record["event.name"] != "service.failed" || record["stage"] != "database.access" || len(record) != 5 {
				t.Fatal("runtime access failure lost its fixed startup stage")
			}
			for _, private := range []string{cfg.User, cfg.Password, cfg.Database, f.dsn, row.ID, owner, denied} {
				if private != "" && strings.Contains(string(output), private) {
					t.Fatal("runtime access failure exposed a private value")
				}
			}
			// Only the operator removes the excessive grant. The installer must
			// preserve operator grants and must not silently broaden its contract.
			if mode.repair != "" {
				execute(mode.repair)
			}
			grantAPIFixtureRuntimeAccess(t, f, cfg.User)
			grantAPIFixtureRuntimeAccess(t, f, cfg.User)
			ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := databaseaccess.VerifyRuntime(ctx, f.runtime); err != nil {
				t.Fatal("runtime access did not recover after installation")
			}
		})
		if t.Failed() {
			return
		}
		stop, address = startApplication(t, binary, f.dsn, brokerConfig, settings...)
		endpoint := address + "/api/hypershell/v1/gateways/" + row.ID
		if code, _ := requestJSON(t, "GET", endpoint, owner, nil); code != 200 {
			t.Fatal("owner lost Gateway after access repair", code)
		}
		if code, _ := requestJSON(t, "GET", endpoint, denied, nil); code != 404 {
			t.Fatal("access repair changed denied requests", code)
		}
		for _, selected := range []struct {
			bearer string
			total  int
		}{{owner, 1}, {denied, 0}} {
			code, body := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways?search="+url.QueryEscape("name = 'access-repair'"), selected.bearer, nil)
			var page struct {
				Total int
				Items []json.RawMessage
			}
			if code != 200 || json.Unmarshal(body, &page) != nil || page.Total != selected.total || len(page.Items) != selected.total {
				t.Fatal("access repair changed filtered Gateway lists", code)
			}
		}
		stop()
	}
}
