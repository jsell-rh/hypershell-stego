package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"encoding/pem"
	"github.com/golang-jwt/jwt/v5"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	"google.golang.org/grpc/metadata"
)

func TestGeneratedCLIWorkflow(t *testing.T) {
	f := database(t)
	_, brokerConfig := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, brokerConfig)
	key, settings := issuer(t)
	rpcIdentity := identity(t, "localhost")
	directory := filepath.Dir(rpcIdentity.config.CAFile)
	settings = append(settings, "STEGO_GRPC_TLS_CERT="+filepath.Join(directory, "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(directory, "server-key.pem"))
	apiBinary := buildApplication(t)
	cliBinary := buildProgram(t, "./out/cli/cmd")
	stop, address, rpcAddress := startBoth(t, apiBinary, f.dsn, brokerConfig, settings...)
	defer func() { stop() }()
	var apiCalls atomic.Int32
	var backend atomic.Value
	backend.Store(address)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		target, err := url.Parse(backend.Load().(string))
		if err != nil {
			t.Error(err)
			return
		}
		httputil.NewSingleHostReverseProxy(target).ServeHTTP(w, r)
	}))
	proxy.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	proxy.StartTLS()
	defer proxy.Close()
	ca := filepath.Join(t.TempDir(), "api-ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0644); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(t.TempDir(), "owner.json")
	tokenFile := filepath.Join(filepath.Dir(cfg), "token")
	owner := token(t, key, "alice", "gateway:creator")
	if err := os.WriteFile(tokenFile, []byte(owner), 0600); err != nil {
		t.Fatal(err)
	}
	run := func(config string, args ...string) ([]byte, string, error) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		command := exec.CommandContext(ctx, cliBinary, args...)
		command.Env = append(os.Environ(), "HYPERSHELL_CONFIG="+config, "GORACE=atexit_sleep_ms=0")
		var output, problem bytes.Buffer
		command.Stdout = &output
		command.Stderr = &problem
		err := command.Run()
		if strings.Contains(problem.String(), "DATA RACE") {
			t.Fatal("CLI data race")
		}
		if bytes.Contains(output.Bytes(), []byte(owner)) || bytes.Contains(problem.Bytes(), []byte(owner)) {
			t.Fatal("CLI exposed token")
		}
		return output.Bytes(), problem.String(), err
	}
	success := func(args ...string) []byte {
		t.Helper()
		data, problem, err := run(cfg, args...)
		if err != nil {
			t.Fatalf("CLI failed: %v %s", err, problem)
		}
		return data
	}
	success("login", "--url", proxy.URL, "--token-file", tokenFile, "--ca-file", ca)
	checkIdentity := func(subject string) {
		t.Helper()
		var report struct {
			Username, Email, Issuer, Subject string
			APIURL                           string    `json:"api_url"`
			ExpiresAt                        time.Time `json:"expires_at"`
		}
		if json.Unmarshal(success("whoami"), &report) != nil || report.Username != subject || report.Subject != subject || report.Email != subject+"@example.test" || report.Issuer != "https://issuer.example" || report.APIURL != proxy.URL || report.ExpiresAt.Before(time.Now()) {
			t.Fatal("CLI identity differs from verified claims", report)
		}
	}
	checkIdentity("alice")
	for _, flag := range []string{"--show-token", "--show-token-decoded"} {
		before := apiCalls.Load()
		if output, _, err := run(cfg, "whoami", flag); err == nil || len(output) != 0 || apiCalls.Load() != before {
			t.Fatal("token output did not require an explicit destination")
		}
		path := filepath.Join(filepath.Dir(cfg), strings.TrimPrefix(flag, "--"))
		if output := success("whoami", flag, "--output-file", path); len(output) != 0 {
			t.Fatal("token export wrote stdout")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("token export is not private")
		}
		if flag == "--show-token" {
			if string(data) != owner+"\n" {
				t.Fatal("export changed the verified token")
			}
		} else {
			var claims map[string]any
			if json.Unmarshal(data, &claims) != nil || claims["sub"] != "alice" || claims["iss"] != "https://issuer.example" {
				t.Fatal("decoded export changed verified claims")
			}
		}
		before = apiCalls.Load()
		if _, _, err := run(cfg, "whoami", flag, "--output-file", path); err == nil || apiCalls.Load() != before {
			t.Fatal("existing token output contacted API")
		}
	}
	expired, err := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{"iss": "https://issuer.example", "sub": "alice", "aud": "hypershell", "preferred_username": "alice", "iat": time.Now().Add(-2 * time.Hour).Unix(), "exp": time.Now().Add(-time.Hour).Unix()}).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(owner, ".")
	parts[2] = "AAAA"
	for _, invalid := range []string{strings.Join(parts, "."), expired} {
		if err := os.WriteFile(tokenFile, []byte(invalid), 0600); err != nil {
			t.Fatal(err)
		}
		for _, flags := range [][]string{nil, {"--show-token"}, {"--show-token-decoded"}} {
			path := filepath.Join(filepath.Dir(cfg), "denied-token")
			args := append([]string{"whoami", "--output-file", path}, flags...)
			if output, problem, err := run(cfg, args...); err == nil || len(output) != 0 || !strings.Contains(problem, "HTTP 401") || strings.Contains(problem, invalid) {
				t.Fatal("invalid token produced identity or token output", err, problem)
			}
			if _, err := os.Stat(path); !os.IsNotExist(err) {
				t.Fatal("failed verification left token output")
			}
		}
	}
	if err := os.WriteFile(tokenFile, []byte(owner), 0600); err != nil {
		t.Fatal(err)
	}
	saved, err := os.ReadFile(cfg)
	if err != nil || bytes.Contains(saved, []byte(owner)) {
		t.Fatal("configuration copied the token")
	}
	created := success("create", "gateway", "--name", "cli-workflow", "--cluster-id", f.cluster, "--release-id", f.release, "--database-id", "", "--server-dns-names", `["cli.example.test"]`)
	var gateway httpapi.Gateway
	if err := json.Unmarshal(created, &gateway); err != nil || gateway.ID == "" {
		t.Fatal("invalid CLI creation response", err)
	}
	readEvent(t, consumer, gateway.ID)
	var ownerGrants int
	if err := f.db.QueryRow(`SELECT count(*) FROM role_bindings b JOIN roles r ON r.id=b.role_id WHERE b.gateway_id=$1 AND b.scope='gateway' AND r.name='gateway:owner'`, gateway.ID).Scan(&ownerGrants); err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "gateways") != 1 || ownerGrants != 1 {
		t.Fatal("CLI creation did not commit resource and grant")
	}
	rpc, connection := grpcClient(t, rpcAddress, rpcIdentity)
	rpcContext, cancelRPC := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancelRPC()
	call := metadata.NewOutgoingContext(rpcContext, metadata.Pairs("authorization", "Bearer "+owner))
	got, err := rpc.GetGateway(call, &pb.GetGatewayRequest{Id: gateway.ID})
	if err != nil || got.Gateway.Name != "cli-workflow" || len(got.Gateway.ServerDnsNames) != 1 {
		t.Fatal("CLI resource differs over gRPC", err)
	}
	connection.Close()
	success("get", "gateway", gateway.ID)
	listed := success("list", "gateways", "--size", "1", "--search", "name = 'cli-workflow'")
	var list struct {
		Total int
		Items []httpapi.Gateway
	}
	if json.Unmarshal(listed, &list) != nil || list.Total != 1 || len(list.Items) != 1 || list.Items[0].ID != gateway.ID {
		t.Fatal("CLI filtered list differs")
	}
	// A new process reads the rotated file; the owner grant does not need the
	// creator role after creation.
	if err := os.WriteFile(tokenFile, []byte(token(t, key, "alice")), 0600); err != nil {
		t.Fatal(err)
	}
	success("get", "gateway", gateway.ID)
	if err := os.WriteFile(tokenFile, []byte(token(t, key, "bob")), 0600); err != nil {
		t.Fatal(err)
	}
	checkIdentity("bob")
	if output, problem, err := run(cfg, "get", "gateway", gateway.ID); err == nil || len(output) != 0 || !strings.Contains(problem, "HTTP 404") {
		t.Fatal("CLI denied read was not opaque")
	}
	listed = success("list", "gateways", "--size", "1")
	if json.Unmarshal(listed, &list) != nil || list.Total != 0 || len(list.Items) != 0 {
		t.Fatal("CLI list exposed another owner")
	}
	if err := os.WriteFile(tokenFile, []byte(owner), 0600); err != nil {
		t.Fatal(err)
	}
	// Complete identity synchronization before rejecting only the final
	// Gateway event. Its failure must roll back all creation records.
	success("get", "gateway", gateway.ID)
	grants := count(t, f.db, "role_bindings")
	databases := count(t, f.db, "managed_databases")
	awaitQueueEmpty(t, f)
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_cli_event CHECK (kind <> 'gateway.created') NOT VALID"); err != nil {
		t.Fatal(err)
	}
	body := filepath.Join(t.TempDir(), "create.json")
	request, _ := json.Marshal(map[string]string{"name": "cli-rollback", "cluster_id": f.cluster, "release_id": f.release, "database_id": ""})
	if err := os.WriteFile(body, request, 0600); err != nil {
		t.Fatal(err)
	}
	if output, problem, err := run(cfg, "create", "gateway", "--body", body); err == nil || len(output) != 0 || !strings.Contains(problem, "HTTP 500") || strings.Contains(problem, "reject_cli_event") {
		t.Fatal("CLI failure response is incorrect")
	}
	if count(t, f.db, "gateways") != 1 || count(t, f.db, "role_bindings") != grants || count(t, f.db, "managed_databases") != databases {
		t.Fatal("CLI failed commit left resource data")
	}
	if _, err := f.db.Exec("ALTER TABLE stego_outbox.messages DROP CONSTRAINT reject_cli_event"); err != nil {
		t.Fatal(err)
	}
	stop()
	stop, address, rpcAddress = startBoth(t, apiBinary, f.dsn, brokerConfig, settings...)
	backend.Store(address)
	checkIdentity("alice")
	success("get", "gateways", gateway.ID)
	if _, _, err := run(cfg, "delete", "gateway", gateway.ID); err == nil {
		t.Fatal("CLI deletion did not require confirmation")
	}
	success("get", "gateway", gateway.ID)
	success("delete", "gateway", gateway.ID, "--yes")
	readGatewayEvent(t, consumer, gateway.ID, "Delete", "gateway.deleted")
	if output, problem, err := run(cfg, "get", "gateway", gateway.ID); err == nil || len(output) != 0 || !strings.Contains(problem, "HTTP 404") {
		t.Fatal("CLI read deleted resource")
	}
	rpc, connection = grpcClient(t, rpcAddress, rpcIdentity)
	defer connection.Close()
	if _, err := rpc.GetGateway(call, &pb.GetGatewayRequest{Id: gateway.ID}); err == nil {
		t.Fatal("gRPC retained deleted Gateway")
	}
	success("logout")
	if _, err := os.Stat(cfg); !os.IsNotExist(err) {
		t.Fatal("CLI logout retained configuration")
	}
	if _, err := os.Stat(tokenFile); err != nil {
		t.Fatal("CLI logout removed the externally owned token")
	}
	t.Log("The generated CLI completed TLS, atomic creation, filtered access, events, restart, and deletion checks")
}
