package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"net"
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
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestGeneratedCLIObservabilityAcrossRestart(t *testing.T) {
	f := database(t)
	_, brokerConfig := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, brokerConfig)
	key, auth := issuer(t)
	signals, exports := newHTTPDiagnosticCollector(t)
	apiEnv := append(append([]string{}, auth...), exports...)
	apiEnv = append(apiEnv, "OTEL_SERVICE_NAME=hypershell-cli-api")
	apiBinary := buildApplication(t)
	cliBinary := buildProgram(t, "./out/cli/cmd")
	stop, address, _, _, apiOutput := startBothWithLogs(t, apiBinary, f.dsn, brokerConfig, apiEnv...)
	var backend atomic.Value
	backend.Store(address)
	proxy := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: proxy.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	tokenFile := filepath.Join(dir, "token")
	owner := token(t, key, "alice", "gateway:creator")
	if err := os.WriteFile(tokenFile, []byte(owner), 0600); err != nil {
		t.Fatal(err)
	}
	cliEnv := append(append([]string{}, exports...), "HYPERSHELL_CONFIG="+filepath.Join(dir, "config.json"), "OTEL_SERVICE_NAME=hypershell-cli", "GORACE=atexit_sleep_ms=0")
	local := ""
	instances := map[string]bool{}
	run := func(outcome string, extra []string, args ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, cliBinary, args...)
		cmd.Env = append(append(append([]string{}, os.Environ()...), cliEnv...), extra...)
		var output, problem bytes.Buffer
		cmd.Stdout = &output
		cmd.Stderr = &problem
		err := cmd.Run()
		if (err != nil) != (outcome == "failure") || ctx.Err() != nil {
			t.Fatal("CLI command result", outcome, err)
		}
		if outcome == "failure" && (output.Len() != 0 || !strings.Contains(problem.String(), "HTTP 404")) {
			t.Fatal("denied command did not return the required result")
		}
		local += problem.String()
		count := 0
		for _, line := range strings.Split(problem.String(), "\n") {
			var record map[string]any
			if json.Unmarshal([]byte(line), &record) == nil && record["event.name"] == "cli.command.completed" {
				count++
				if record["outcome"] != outcome {
					t.Fatal("wrong command outcome")
				}
				instance, _ := record["service.instance.id"].(string)
				if instance == "" || instances[instance] {
					t.Fatal("CLI process has no distinct runtime identity")
				}
				instances[instance] = true
			}
		}
		if count != 1 {
			t.Fatal("generated CLI has no common completion record", count)
		}
		return output.Bytes()
	}
	run("success", nil, "login", "--url", proxy.URL, "--token-file", tokenFile, "--ca-file", ca)
	data := run("success", nil, "create", "gateway", "--name", "private-cli-gateway", "--cluster-id", f.cluster, "--release-id", f.release, "--database-id", "")
	var gateway httpapi.Gateway
	if json.Unmarshal(data, &gateway) != nil || gateway.ID == "" {
		t.Fatal("CLI stdout is not a Gateway")
	}
	var grants int
	if err := f.db.QueryRow(`SELECT count(*) FROM role_bindings b JOIN roles r ON r.id=b.role_id WHERE b.gateway_id=$1 AND r.name='gateway:owner'`, gateway.ID).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("CLI creation lost its owner grant", grants, err)
	}
	if readEvent(t, consumer, gateway.ID) == "" {
		t.Fatal("CLI Gateway event missing")
	}
	awaitQueueEmpty(t, f)
	if err := os.WriteFile(tokenFile, []byte(token(t, key, "mallory")), 0600); err != nil {
		t.Fatal(err)
	}
	run("failure", nil, "get", "gateway", gateway.ID)
	if err := os.WriteFile(tokenFile, []byte(owner), 0600); err != nil {
		t.Fatal(err)
	}
	stop()
	local += apiOutput()
	stop, address, _, _, apiOutput = startBothWithLogs(t, apiBinary, f.dsn, brokerConfig, apiEnv...)
	backend.Store(address)
	data = run("success", nil, "get", "gateway", gateway.ID)
	var got httpapi.Gateway
	if json.Unmarshal(data, &got) != nil || got.ID != gateway.ID || got.Name != gateway.Name {
		t.Fatal("restart changed the retained Gateway")
	}
	// The CLI must complete its request when its own collector is unavailable.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	unavailable := listener.Addr().String()
	listener.Close()
	started := time.Now()
	data = run("success", []string{"OTEL_EXPORTER_OTLP_ENDPOINT=https://" + unavailable}, "get", "gateway", gateway.ID)
	got = httpapi.Gateway{}
	if time.Since(started) > 6*time.Second || json.Unmarshal(data, &got) != nil || got.ID != gateway.ID || got.Name != gateway.Name {
		t.Fatal("collector loss prevented CLI completion")
	}
	stop()
	local += apiOutput()
	checkCLIObservability(t, signals, local, []string{gateway.ID, owner, "private-cli-gateway"})
}

func checkCLIObservability(t *testing.T, c *httpDiagnosticCollector, local string, private []string) {
	t.Helper()
	for _, secret := range private {
		if strings.Contains(local, secret) {
			t.Fatal("CLI diagnostic output exposed private data")
		}
	}
	check := func(m proto.Message) {
		data, _ := proto.Marshal(m)
		for _, secret := range private {
			if bytes.Contains(data, []byte(secret)) {
				t.Fatal("CLI telemetry exposed private data")
			}
		}
	}
	spans := map[string]*tracepb.Span{}
	roots := map[string]*tracepb.Span{}
	logs := map[string]*logpb.LogRecord{}
	for len(c.traces.received) > 0 {
		batch := <-c.traces.received
		check(batch)
		for _, res := range batch.ResourceSpans {
			for _, scope := range res.ScopeSpans {
				for _, span := range scope.Spans {
					id := hex.EncodeToString(span.SpanId)
					spans[id] = span
					if scope.Scope.Name == "stego/cli" {
						telemetryInstance(t, res.Resource.Attributes, "hypershell-cli")
						roots[id] = span
					}
				}
			}
		}
	}
	for len(c.logs.received) > 0 {
		batch := <-c.logs.received
		check(batch)
		for _, res := range batch.ResourceLogs {
			for _, scope := range res.ScopeLogs {
				if scope.Scope.Name == "stego/cli" {
					for _, record := range scope.LogRecords {
						logs[hex.EncodeToString(record.SpanId)] = record
					}
				}
			}
		}
	}
	measured := map[string]bool{}
	for len(c.metrics.received) > 0 {
		batch := <-c.metrics.received
		check(batch)
		for _, res := range batch.ResourceMetrics {
			for _, scope := range res.ScopeMetrics {
				if scope.Scope.Name == "stego/cli" {
					for _, m := range scope.Metrics {
						if m.Name == "stego.cli.command.duration" {
							for _, p := range m.GetHistogram().DataPoints {
								if p.Count > 0 {
									measured[signalAttribute(p.Attributes, "outcome").GetStringValue()] = true
								}
							}
						}
					}
				}
			}
		}
	}
	found := map[string]bool{}
	for _, server := range spans {
		if server.Kind != tracepb.Span_SPAN_KIND_SERVER {
			continue
		}
		client := spans[hex.EncodeToString(server.ParentSpanId)]
		if client == nil || client.Kind != tracepb.Span_SPAN_KIND_CLIENT {
			continue
		}
		id := hex.EncodeToString(client.ParentSpanId)
		root := roots[id]
		record := logs[id]
		if root == nil || record == nil || record.EventName != "cli.command.completed" || !bytes.Equal(root.TraceId, client.TraceId) || !bytes.Equal(root.TraceId, server.TraceId) || !bytes.Equal(root.TraceId, record.TraceId) {
			continue
		}
		outcome := signalAttribute(root.Attributes, "outcome").GetStringValue()
		if outcome != signalAttribute(record.Attributes, "outcome").GetStringValue() {
			t.Fatal("CLI log differs from span")
		}
		if root.Name != "cli.command" || root.Kind != tracepb.Span_SPAN_KIND_INTERNAL || len(root.ParentSpanId) != 0 {
			t.Fatal("invalid CLI root")
		}
		found[outcome] = true
	}
	for _, outcome := range []string{"success", "failure"} {
		if !found[outcome] || !measured[outcome] {
			t.Fatal("CLI request has no complete trace, log, and metric", outcome)
		}
	}
}
