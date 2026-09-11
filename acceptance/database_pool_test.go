package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"io"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestGatewayDatabasePoolWaitCancellationAndRestart(t *testing.T) {
	f := database(t)
	_, brokerConfig := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, brokerConfig)
	key, settings := issuer(t)
	settings = append(settings, "STEGO_DATABASE_MAX_OPEN_CONNECTIONS=2", "STEGO_DATABASE_MAX_IDLE_CONNECTIONS=2")
	dsn := f.dsn + " application_name=stego-pool-probe"
	if strings.HasPrefix(f.dsn, "postgres://") || strings.HasPrefix(f.dsn, "postgresql://") {
		address, err := url.Parse(f.dsn)
		if err != nil {
			t.Fatal(err)
		}
		query := address.Query()
		query.Set("application_name", "stego-pool-probe")
		address.RawQuery = query.Encode()
		dsn = address.String()
	}
	binary := buildApplication(t)
	owner := token(t, key, "alice", "gateway:creator")
	stop, address := startApplication(t, binary, dsn, brokerConfig, settings...)
	defer func() { stop() }()
	input, err := json.Marshal(f.request("private-pool-gateway"))
	if err != nil {
		t.Fatal(err)
	}
	code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, input)
	var gateway httpapi.Gateway
	if code != 201 || json.Unmarshal(body, &gateway) != nil || gateway.ID == "" {
		t.Fatal("Gateway creation failed", code)
	}
	var grants int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE gateway_id=$1 AND scope='gateway'", gateway.ID).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("atomic Gateway owner grant missing", grants, err)
	}
	if readEvent(t, consumer, gateway.ID) == "" {
		t.Fatal("Gateway event missing")
	}
	awaitQueueEmpty(t, f)
	endpoint := address + "/api/hypershell/v1/gateways/" + gateway.ID
	if code, _ := requestJSON(t, "GET", endpoint, owner, nil); code != 200 {
		t.Fatal("owner read failed", code)
	}
	lock, err := f.db.BeginTx(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err = lock.Exec("LOCK TABLE gateways IN ACCESS EXCLUSIVE MODE"); err != nil {
		t.Fatal(err)
	}
	type result struct {
		code int
		err  error
	}
	client := &http.Client{Timeout: 8 * time.Second}
	start := func(ctx context.Context) <-chan result {
		resultCh := make(chan result, 1)
		go func() {
			request, err := http.NewRequestWithContext(ctx, "GET", endpoint, nil)
			if err != nil {
				resultCh <- result{err: err}
				return
			}
			request.Header.Set("Authorization", "Bearer "+owner)
			response, err := client.Do(request)
			if err != nil {
				resultCh <- result{err: err}
				return
			}
			_, err = io.Copy(io.Discard, response.Body)
			response.Body.Close()
			resultCh <- result{code: response.StatusCode, err: err}
		}()
		return resultCh
	}
	ctx, cancel := context.WithTimeout(context.Background(), 7*time.Second)
	defer cancel()
	first, second := start(ctx), start(ctx)
	counts := func() (int, int) {
		t.Helper()
		var open, blocked int
		queryCtx, done := context.WithTimeout(context.Background(), time.Second)
		defer done()
		err := f.db.QueryRowContext(queryCtx, `SELECT count(*), count(*) FILTER (WHERE wait_event_type='Lock') FROM pg_stat_activity WHERE datname=current_database() AND application_name='stego-pool-probe' AND query NOT LIKE 'LISTEN %'`).Scan(&open, &blocked)
		if err != nil {
			t.Fatal(err)
		}
		if open > 2 {
			t.Fatal("generated database pool exceeded its connection limit", open)
		}
		return open, blocked
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		_, blocked := counts()
		if blocked == 2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Gateway reads did not occupy both pool connections")
		}
		time.Sleep(10 * time.Millisecond)
	}
	short, done := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer done()
	limit := time.NewTimer(2 * time.Second)
	defer limit.Stop()
	third := start(short)
	waiting := true
	for waiting {
		counts()
		select {
		case got := <-third:
			if !errors.Is(got.err, context.DeadlineExceeded) {
				t.Fatal("waiting request did not retain its deadline", got.code, got.err)
			}
			waiting = false
		case <-limit.C:
			t.Fatal("waiting request ignored its deadline")
		case <-time.After(10 * time.Millisecond):
		}
	}
	done()
	if err := lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	for _, pending := range []<-chan result{first, second} {
		select {
		case got := <-pending:
			if got.err != nil || got.code != 200 {
				t.Fatal("Gateway read did not recover after pool pressure", got.code, got.err)
			}
		case <-ctx.Done():
			t.Fatal("pool pressure blocked request recovery")
		}
	}
	counts()
	if code, _ := requestJSON(t, "GET", endpoint, token(t, key, "mallory"), nil); code != 404 {
		t.Fatal("pool recovery changed access rules", code)
	}
	stop()
	stop, address = startApplication(t, binary, dsn, brokerConfig, settings...)
	endpoint = address + "/api/hypershell/v1/gateways/" + gateway.ID
	if code, _ := requestJSON(t, "GET", endpoint, owner, nil); code != 200 {
		t.Fatal("restart lost the Gateway", code)
	}
	counts()
}

func TestGatewayRejectsInvalidDatabasePoolSettings(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Env = applicationEnvironment(t, f.dsn, config, "STEGO_DATABASE_MAX_OPEN_CONNECTIONS=private-pool-value")
	output, err := command.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || ctx.Err() != nil {
		t.Fatal("invalid pool settings did not stop startup", err)
	}
	for _, private := range []string{"private-pool-value", f.dsn, "starting server", "gRPC server started"} {
		if strings.Contains(string(output), private) {
			t.Fatal("pool startup exposed private data or started a listener")
		}
	}
	failures := 0
	for _, line := range strings.Split(string(output), "\n") {
		var record map[string]any
		if json.Unmarshal([]byte(line), &record) != nil || record["event.name"] != "service.failed" {
			continue
		}
		failures++
		if len(record) != 5 || record["stage"] != "database.open" || record["severity"] != "ERROR" || record["message"] != "Service failed" {
			t.Fatal("invalid pool settings lost the safe failure stage")
		}
	}
	if failures != 1 {
		t.Fatal("invalid pool startup must report one failure", failures)
	}
}
