package acceptance

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGatewayDatabaseSecretFileAndCredentialRotation(t *testing.T) {
	ca := os.Getenv("STEGO_TEST_POSTGRES_CA_FILE")
	if ca == "" {
		if os.Getenv("STEGO_REQUIRE_POSTGRES") == "1" {
			t.Fatal("database secret acceptance requires STEGO_TEST_POSTGRES_CA_FILE")
		}
		t.Skip("set STEGO_TEST_POSTGRES_CA_FILE for database secret acceptance")
	}
	f := database(t)
	cfg, err := pgx.ParseConfig(f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	role := "secret_" + strings.TrimPrefix(cfg.Database, "hypershell_test_")
	identifier := pgx.Identifier{role}.Sanitize()
	password := func() string {
		data := make([]byte, 24)
		if _, err := rand.Read(data); err != nil {
			t.Fatal(err)
		}
		return hex.EncodeToString(data)
	}
	first, second := password(), password()
	if _, err := f.db.Exec("CREATE ROLE " + identifier + " LOGIN PASSWORD '" + first + "'"); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, err := f.db.Exec("DROP OWNED BY " + identifier); err != nil {
			t.Error(err)
		}
		if _, err := f.db.Exec("DROP ROLE " + identifier); err != nil {
			t.Error(err)
		}
	})
	for _, statement := range []string{
		"GRANT CONNECT ON DATABASE " + pgx.Identifier{cfg.Database}.Sanitize() + " TO " + identifier,
		"GRANT USAGE ON SCHEMA public, stego_outbox TO " + identifier,
		"GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public, stego_outbox TO " + identifier,
		"GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public, stego_outbox TO " + identifier,
	} {
		if _, err := f.db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	dsnFor := func(password string) string {
		address := &url.URL{Scheme: "postgres", Host: net.JoinHostPort("localhost", strconv.Itoa(int(cfg.Port))), Path: "/" + cfg.Database, User: url.UserPassword(role, password)}
		address.RawQuery = url.Values{"sslmode": {"verify-full"}, "sslrootcert": {ca}, "application_name": {"stego-file-secret"}}.Encode()
		return address.String()
	}
	probe, err := pgx.ParseConfig(dsnFor(first + "invalid"))
	if err != nil {
		t.Fatal("invalid password probe configuration")
	}
	probeContext, cancelProbe := context.WithTimeout(context.Background(), 3*time.Second)
	connection, probeErr := pgx.ConnectConfig(probeContext, probe)
	if connection != nil {
		connection.Close(probeContext)
	}
	cancelProbe()
	var rejection *pgconn.PgError
	if !errors.As(probeErr, &rejection) || rejection.Code != "28P01" {
		t.Fatal("the database fixture must reject an invalid password")
	}
	directory := t.TempDir()
	name := filepath.Join(directory, "database-url")
	if err := os.Symlink("..data/url", name); err != nil {
		t.Fatal(err)
	}
	project := func(revision, password string) {
		t.Helper()
		path := filepath.Join(directory, revision)
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(path, "url"), []byte(dsnFor(password)+"\n"), 0440); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(revision, filepath.Join(directory, "..next")); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(filepath.Join(directory, "..next"), filepath.Join(directory, "..data")); err != nil {
			t.Fatal(err)
		}
	}
	project("first", first)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	rpcIdentity := identity(t, "localhost")
	rpcDirectory := filepath.Dir(rpcIdentity.config.CAFile)
	settings = append(settings, "DATABASE_URL_FILE="+name, "STEGO_DATABASE_ALLOW_INSECURE_LOOPBACK=0", "STEGO_GRPC_TLS_CERT="+filepath.Join(rpcDirectory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(rpcDirectory, "server-key.pem"))
	binary := buildApplication(t)
	stop, address, rpcAddress := startBoth(t, binary, "", config, settings...)
	defer func() { stop() }()
	owner := token(t, key, "alice", "gateway:creator")
	other := token(t, key, "mallory")
	create := func(name string) string {
		t.Helper()
		input, err := json.Marshal(f.request(name))
		if err != nil {
			t.Fatal(err)
		}
		code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, input)
		var row struct {
			ID string `json:"id"`
		}
		if code != 201 || json.Unmarshal(body, &row) != nil || row.ID == "" {
			t.Fatal("file-configured Gateway creation failed", code)
		}
		var grants int
		if err := f.db.QueryRow(`SELECT count(*) FROM role_bindings b JOIN roles r ON r.id=b.role_id JOIN users u ON u.id=b.user_id WHERE b.gateway_id=$1 AND b.scope='gateway' AND r.name='gateway:owner' AND u.username='alice'`, row.ID).Scan(&grants); err != nil || grants != 1 {
			t.Fatal("file-configured Gateway owner grant missing", err)
		}
		readEvent(t, consumer, row.ID)
		awaitQueueEmpty(t, f)
		return row.ID
	}
	id := create("secret-gateway")
	check := func() {
		t.Helper()
		var total, insecure, listeners int
		if err := f.db.QueryRow(`SELECT count(*),count(*) FILTER(WHERE s.ssl IS DISTINCT FROM true),count(*) FILTER(WHERE a.query LIKE 'LISTEN %') FROM pg_stat_activity a LEFT JOIN pg_stat_ssl s ON s.pid=a.pid WHERE a.datname=current_database() AND a.application_name='stego-file-secret'`).Scan(&total, &insecure, &listeners); err != nil || total < 2 || insecure != 0 || listeners != 1 {
			t.Fatal("secret source lost TLS pool or listener", err)
		}
		endpoint := address + "/api/hypershell/v1/gateways/" + id
		if code, _ := requestJSON(t, "GET", endpoint, owner, nil); code != 200 {
			t.Fatal("file-configured Gateway read failed", code)
		}
		if code, _ := requestJSON(t, "GET", endpoint, other, nil); code != 404 {
			t.Fatal("file-configured Gateway access changed", code)
		}
		rpc, connection := grpcClient(t, rpcAddress, rpcIdentity)
		defer connection.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		call := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+owner))
		got, err := rpc.GetGateway(call, &pb.GetGatewayRequest{Id: id})
		if err != nil || got.GetGateway().GetMetadata().GetId() != id {
			t.Fatal("file-configured Gateway gRPC read failed", err)
		}
		call = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+other))
		if _, err := rpc.GetGateway(call, &pb.GetGatewayRequest{Id: id}); status.Code(err) != codes.NotFound {
			t.Fatal("file-configured Gateway gRPC access changed", err)
		}
	}
	check()
	stop()
	if _, err := f.db.Exec("ALTER ROLE " + identifier + " PASSWORD '" + second + "'"); err != nil {
		t.Fatal(err)
	}
	fail := func(stage, dsn, path string) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, binary)
		command.Env = applicationEnvironment(t, dsn, config, append(append([]string{}, settings...), "DATABASE_URL_FILE="+path)...)
		output, err := command.CombinedOutput()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || ctx.Err() != nil {
			t.Fatal("invalid database source did not stop startup", err)
		}
		var record map[string]any
		if json.Unmarshal(output, &record) != nil || record["event.name"] != "service.failed" || record["stage"] != stage || len(record) != 5 {
			t.Fatal("database source failure lost its safe stage")
		}
		for _, private := range []string{first, second, role, cfg.Database, name, path, ca} {
			if private != "" && strings.Contains(string(output), private) {
				t.Fatal("database source failure exposed private data")
			}
		}
	}
	fail("database.ping", "", name)
	project("second", second)
	fail("database.configure", dsnFor(second), name)
	fail("database.configure", "", filepath.Join(directory, "private-missing"))
	fifo := filepath.Join(directory, "private-fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	fail("database.configure", "", fifo)
	invalid := filepath.Join(directory, "private-invalid")
	if err := os.WriteFile(invalid, []byte(dsnFor(second)), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(invalid, 0644); err != nil {
		t.Fatal(err)
	}
	fail("database.configure", "", invalid)
	if err := os.Chmod(invalid, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(invalid, []byte(strings.Repeat("a", 65537)), 0600); err != nil {
		t.Fatal(err)
	}
	fail("database.configure", "", invalid)
	if err := os.WriteFile(invalid, []byte(strings.Replace(dsnFor(second), "sslmode=verify-full", "sslmode=require", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	fail("database.open", "", invalid)
	stop, address, rpcAddress = startBoth(t, binary, "", config, settings...)
	check()
	create("rotated-secret-gateway")
}
