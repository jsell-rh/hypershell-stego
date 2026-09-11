package acceptance

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"io"
	"net"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func TestGatewayDatabaseStartupDeadlineAndRecovery(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	consumer := kafkaConsumer(t, config)
	key, settings := issuer(t)
	binary := buildApplication(t)
	stop, address := startApplication(t, binary, f.dsn, config, settings...)
	defer func() { stop() }()
	owner := token(t, key, "alice", "gateway:creator")
	input, err := json.Marshal(f.request("startup-recovery"))
	if err != nil {
		t.Fatal(err)
	}
	code, body := requestJSON(t, "POST", address+"/api/hypershell/v1/gateways", owner, input)
	var row struct {
		ID string `json:"id"`
	}
	if code != 201 || json.Unmarshal(body, &row) != nil || row.ID == "" {
		t.Fatal("Gateway creation failed before the database fault", code)
	}
	var grants int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE gateway_id=$1 AND scope='gateway'", row.ID).Scan(&grants); err != nil || grants != 1 {
		t.Fatal("Gateway owner grant missing", err)
	}
	readEvent(t, consumer, row.ID)
	awaitQueueEmpty(t, f)
	stop()
	for _, authenticated := range []bool{false, true} {
		dsn, reached := stalledStartupDatabase(t, authenticated)
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		command := exec.CommandContext(ctx, binary)
		command.Env = applicationEnvironment(t, dsn, config, settings...)
		started := time.Now()
		output, err := command.CombinedOutput()
		elapsed := time.Since(started)
		contextErr := ctx.Err()
		cancel()
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || contextErr != nil || elapsed > 7*time.Second {
			t.Fatal("stalled database did not stop startup within its deadline", authenticated, elapsed, err)
		}
		select {
		case <-reached:
		default:
			t.Fatal("database fault did not reach the required protocol phase")
		}
		failures := 0
		for _, line := range strings.Split(string(output), "\n") {
			var record map[string]any
			if json.Unmarshal([]byte(line), &record) == nil && record["event.name"] == "service.failed" {
				failures++
				if record["stage"] != "database.ping" || len(record) != 5 {
					t.Fatal("database startup lost its safe failure stage")
				}
			}
		}
		if failures != 1 || strings.Contains(string(output), "private-startup") || strings.Contains(string(output), "starting server") || strings.Contains(string(output), "gRPC server started") {
			t.Fatal("database startup exposed private data or started a listener")
		}
	}
	// A process stop must cancel the ping before the database deadline expires.
	stalled, reached := stalledStartupDatabase(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, binary)
	command.Env = applicationEnvironment(t, stalled, config, settings...)
	var logs bytes.Buffer
	command.Stdout, command.Stderr = &logs, &logs
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case <-reached:
	case err := <-done:
		t.Fatal("process stopped before the ping cancellation probe", err)
	case <-ctx.Done():
		<-done
		t.Fatal("process did not reach the ping cancellation probe")
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 1 || !strings.Contains(logs.String(), "database.ping") {
			t.Fatal("process stop did not cancel the database ping", err)
		}
	case <-time.After(2 * time.Second):
		command.Process.Kill()
		<-done
		t.Fatal("process stop left the startup ping running")
	}
	stop, address = startApplication(t, binary, f.dsn, config, settings...)
	endpoint := address + "/api/hypershell/v1/gateways/" + row.ID
	if code, _ := requestJSON(t, "GET", endpoint, owner, nil); code != 200 {
		t.Fatal("database recovery lost the stored Gateway", code)
	}
	if code, _ := requestJSON(t, "GET", endpoint, token(t, key, "mallory"), nil); code != 404 {
		t.Fatal("database recovery changed access rules", code)
	}
}

// Stop before authentication, or accept startup and stop at the first query.
func stalledStartupDatabase(t *testing.T, authenticated bool) (string, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	reached, stopped := make(chan struct{}), make(chan struct{})
	var workers sync.WaitGroup
	var once sync.Once
	go func() {
		defer close(stopped)
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				defer conn.Close()
				stopClose := context.AfterFunc(ctx, func() { conn.Close() })
				defer stopClose()
				if authenticated {
					var length [4]byte
					if _, err := io.ReadFull(conn, length[:]); err != nil {
						return
					}
					size := binary.BigEndian.Uint32(length[:])
					if size < 8 || size > 8192 {
						return
					}
					if _, err := io.CopyN(io.Discard, conn, int64(size)-4); err != nil {
						return
					}
					if _, err := conn.Write([]byte{'R', 0, 0, 0, 8, 0, 0, 0, 0, 'Z', 0, 0, 0, 5, 'I'}); err != nil {
						return
					}
					var query [1]byte
					if _, err := io.ReadFull(conn, query[:]); err != nil || query[0] != 'Q' {
						return
					}
				}
				once.Do(func() { close(reached) })
				<-ctx.Done()
			}()
		}
	}()
	t.Cleanup(func() { cancel(); listener.Close(); <-stopped; workers.Wait() })
	dsn := "postgres://private-startup-user:private-startup-password@" + listener.Addr().String() + "/private-startup-db?sslmode=disable&connect_timeout=0"
	return dsn, reached
}
