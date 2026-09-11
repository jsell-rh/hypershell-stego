package acceptance

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
)

func TestGeneratedStartupFailurePrivacy(t *testing.T) {
	binary := buildApplication(t)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	listener.Close()
	for _, tc := range []struct{ name, dsn, stage string }{
		{"missing", "", "database.configure"},
		{"URL port", "postgres://private-user:private-password@localhost:bad/private-database", "database.open"},
		{"keyword quote", "host=localhost password='private-password", "database.open"},
		{"connection", "postgres://private-user:private-password@" + address + "/private-database?sslmode=disable&connect_timeout=1", "database.ping"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, binary)
			command.Env = append(os.Environ(), "DATABASE_URL="+tc.dsn)
			output, err := command.CombinedOutput()
			if ctx.Err() != nil {
				t.Fatal("startup failure did not stop the process")
			}
			if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 {
				t.Fatal("startup failure did not return exit code 1", err)
			}
			for _, secret := range []string{"private-user", "private-password", "private-database", address, "main.go:", "cannot parse", "failed to connect"} {
				if strings.Contains(string(output), secret) {
					t.Fatal("startup output exposed private data", secret)
				}
			}
			var record map[string]any
			if err := json.Unmarshal(output, &record); err != nil {
				t.Fatal("startup failure was not one JSON record")
			}
			if len(record) != 5 || record["event.name"] != "service.failed" || record["severity"] != "ERROR" || record["stage"] != tc.stage || record["message"] != "Service failed" {
				t.Fatal("invalid startup failure record", record)
			}
			if stamp, ok := record["timestamp"].(string); !ok {
				t.Fatal("missing timestamp")
			} else if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
				t.Fatal("invalid timestamp")
			}
		})
	}
}

func TestGatewayDatabaseFailureLogPrivacy(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	settings = append(settings, "OTEL_EXPORTER_OTLP_ENDPOINT=", "OTEL_SERVICE_NAME=hypershell-database-privacy")
	stop, address, _, _, output := startBothWithLogs(t, buildApplication(t), f.dsn, config, settings...)
	if _, err := f.db.Exec(`CREATE FUNCTION private_reject_gateway() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'private-database-error'; END $$; CREATE TRIGGER private_reject_gateway BEFORE INSERT ON gateways FOR EACH ROW EXECUTE FUNCTION private_reject_gateway()`); err != nil {
		t.Fatal(err)
	}
	creator := token(t, key, "alice", "gateway:creator")
	path := address + "/api/hypershell/v1/gateways"
	body, err := json.Marshal(f.request("private-gateway-query"))
	if err != nil {
		t.Fatal(err)
	}
	status, response := requestJSON(t, "POST", path, creator, body)
	if status != http.StatusInternalServerError {
		t.Fatalf("database failure returned %d", status)
	}
	ownerGrants := func() int {
		var n int
		if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE scope='gateway'").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	if count(t, f.db, "gateways") != 0 || ownerGrants() != 0 {
		t.Fatal("failed creation did not roll back Gateway and owner grant")
	}
	if _, err := f.db.Exec(`DROP TRIGGER private_reject_gateway ON gateways; DROP FUNCTION private_reject_gateway()`); err != nil {
		t.Fatal(err)
	}
	status, data := requestJSON(t, "POST", path, creator, body)
	if status != http.StatusCreated {
		t.Fatalf("database recovery returned %d", status)
	}
	var gateway httpapi.Gateway
	if err := json.Unmarshal(data, &gateway); err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "gateways") != 1 || ownerGrants() != 1 {
		t.Fatal("recovery did not commit Gateway and owner grant")
	}
	if readEvent(t, consumer, gateway.ID) == "" {
		t.Fatal("recovery lost its event")
	}
	awaitQueueEmpty(t, f)
	stop()
	for _, private := range []string{"private-database-error", "private-gateway-query", creator, "INSERT INTO", "main.go:", "store.go:"} {
		if strings.Contains(output(), private) || strings.Contains(string(response), private) {
			t.Fatal("database failure exposed private data", private)
		}
	}
	checkRuntimeLogs(t, output(), "hypershell-database-privacy")
}
