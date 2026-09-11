package acceptance

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestGatewayDatabaseTLSAndListenerAcrossRestart(t *testing.T) {
	ca := os.Getenv("STEGO_TEST_POSTGRES_CA_FILE")
	if ca == "" {
		if os.Getenv("STEGO_REQUIRE_POSTGRES") == "1" {
			t.Fatal("database TLS acceptance requires STEGO_TEST_POSTGRES_CA_FILE")
		}
		t.Skip("set STEGO_TEST_POSTGRES_CA_FILE for database TLS acceptance")
	}
	f := database(t)
	cfg, err := pgx.ParseConfig(f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	dsnFor := func(host, mode, root string) string {
		address := &url.URL{Scheme: "postgres", Host: net.JoinHostPort(host, strconv.Itoa(int(cfg.Port))), Path: "/" + cfg.Database, User: url.UserPassword(cfg.User, cfg.Password)}
		query := url.Values{"sslmode": {mode}, "sslrootcert": {root}, "application_name": {"stego-tls-gateway"}}
		address.RawQuery = query.Encode()
		return address.String()
	}
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	settings = append(settings, "STEGO_DATABASE_ALLOW_INSECURE_LOOPBACK=0")
	rpcIdentity := identity(t, "localhost")
	directory := filepath.Dir(rpcIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	binary := buildApplication(t)
	dsn := dsnFor("localhost", "verify-full", ca)
	stop, address, rpcAddress := startBoth(t, binary, dsn, config, settings...)
	defer func() { stop() }()
	owner := token(t, key, "alice", "gateway:creator")
	input, err := json.Marshal(f.request("tls-gateway"))
	if err != nil {
		t.Fatal(err)
	}
	code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, input)
	var row struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(body, &row) != nil || row.ID == "" {
		t.Fatal("TLS Gateway creation failed", code)
	}
	var grants int
	if err := f.db.QueryRow(`SELECT count(*) FROM role_bindings b JOIN roles r ON r.id=b.role_id JOIN users u ON u.id=b.user_id WHERE b.gateway_id=$1 AND b.scope='gateway' AND r.name='gateway:owner' AND u.username='alice'`, row.ID).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("TLS Gateway owner grant missing", err)
	}
	readEvent(t, consumer, row.ID)
	awaitQueueEmpty(t, f)
	check := func() {
		t.Helper()
		var total, insecure, listeners int
		err := f.db.QueryRow(`SELECT count(*),count(*) FILTER(WHERE s.ssl IS DISTINCT FROM true),count(*) FILTER(WHERE a.query LIKE 'LISTEN %') FROM pg_stat_activity a LEFT JOIN pg_stat_ssl s ON s.pid=a.pid WHERE a.datname=current_database() AND a.application_name='stego-tls-gateway'`).Scan(&total, &insecure, &listeners)
		if err != nil || total < 2 || insecure != 0 || listeners != 1 {
			t.Fatal("pool or event listener lost TLS", total, insecure, listeners, err)
		}
		endpoint := address + "/api/hypershell/v1/gateways/" + row.ID
		if code, _ := requestJSON(t, "GET", endpoint, owner, nil); code != 200 {
			t.Fatal("TLS Gateway read failed", code)
		}
		if code, _ := requestJSON(t, "GET", endpoint, token(t, key, "mallory"), nil); code != 404 {
			t.Fatal("TLS Gateway access changed", code)
		}
		for _, item := range []struct {
			bearer string
			total  int
		}{{owner, 1}, {token(t, key, "mallory"), 0}} {
			code, body := requestJSON(t, "GET", address+"/api/hypershell/v1/gateways?size=1&search="+url.QueryEscape("name = 'tls-gateway'"), item.bearer, nil)
			var list struct {
				Total int               `json:"total"`
				Items []json.RawMessage `json:"items"`
			}
			if code != 200 || json.Unmarshal(body, &list) != nil || list.Total != item.total || len(list.Items) != item.total {
				t.Fatal("TLS Gateway filtered list changed", code)
			}
		}
		rpc, connection := grpcClient(t, rpcAddress, rpcIdentity)
		defer connection.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		call := metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+owner))
		got, err := rpc.GetGateway(call, &pb.GetGatewayRequest{Id: row.ID})
		if err != nil || got.GetGateway().GetMetadata().GetId() != row.ID {
			t.Fatal("TLS Gateway gRPC read failed", err)
		}
		call = metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+token(t, key, "mallory")))
		if _, err := rpc.GetGateway(call, &pb.GetGatewayRequest{Id: row.ID}); status.Code(err) != codes.NotFound {
			t.Fatal("TLS Gateway gRPC access changed", err)
		}
	}
	check()
	stop()
	wrongCA := identity(t, "localhost").config.CAFile
	for _, tc := range []struct{ host, mode, ca, stage string }{
		{"127.0.0.1", "require", ca, "database.open"},
		{"127.0.0.1", "verify-ca", ca, "database.open"},
		{"127.0.0.1", "prefer", ca, "database.open"},
		{"127.0.0.1", "disable", "", "database.open"},
		{"127.0.0.1", "verify-full", ca, "database.ping"},
		{"localhost", "verify-full", wrongCA, "database.ping"},
	} {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		command := exec.CommandContext(ctx, binary)
		command.Env = applicationEnvironment(t, dsnFor(tc.host, tc.mode, tc.ca), config, settings...)
		output, err := command.CombinedOutput()
		contextErr := ctx.Err()
		cancel()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || contextErr != nil {
			t.Fatal("unsafe database transport did not stop startup", tc.mode, err)
		}
		var record map[string]any
		if json.Unmarshal(output, &record) != nil || record["event.name"] != "service.failed" || record["stage"] != tc.stage || len(record) != 5 {
			t.Fatal("database TLS failure lost its safe stage", tc.mode)
		}
		for _, private := range []string{cfg.Password, cfg.Database, ca, wrongCA} {
			if private != "" && strings.Contains(string(output), private) {
				t.Fatal("database TLS failure exposed private data")
			}
		}
	}
	stop, address, rpcAddress = startBoth(t, binary, dsn, config, settings...)
	check()
}
