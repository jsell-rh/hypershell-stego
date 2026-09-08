package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/twmb/franz-go/pkg/kgo"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestGeneratedRuntimeDeliversGatewayEventsAcrossRestart(t *testing.T) {
	f := database(t)
	_, config := broker(t, identity(t, "localhost"))
	binary := buildApplication(t)
	consumer := kafkaConsumer(t, config)
	p := principal("alice", "gateway:creator")
	first, err := f.service.Create(context.Background(), p, f.request("before-start"))
	if err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "stego_outbox.messages") != 1 {
		t.Fatal("event was lost before runtime start")
	}
	stop := startRuntime(t, binary, f.dsn, config)
	firstMessage := readEvent(t, consumer, first.ID)
	awaitQueueEmpty(t, f)
	stop()
	// Close the application connection, then open a new store and service.
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}
	orm, err := gorm.Open(postgres.Open(f.dsn), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	db, err := orm.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f.db = db
	service, err := gateways.New(storage.NewStore(orm))
	if err != nil {
		t.Fatal(err)
	}
	got, err := service.Get(context.Background(), principal("alice"), first.ID)
	if err != nil || got.Namespace != first.Namespace {
		t.Fatalf("restart lost Gateway or owner grant: %v", err)
	}
	second, err := service.Create(context.Background(), p, f.request("while-stopped"))
	if err != nil {
		t.Fatal(err)
	}
	if count(t, db, "stego_outbox.messages") != 1 {
		t.Fatal("offline event is not durable")
	}
	stop = startRuntime(t, binary, f.dsn, config)
	secondMessage := readEvent(t, consumer, second.ID)
	awaitQueueEmpty(t, f)
	stop()
	if firstMessage == "" || secondMessage == "" || firstMessage == secondMessage {
		t.Fatal("distinct events did not keep distinct message IDs")
	}
}

func startRuntime(t *testing.T, binary, dsn string, config Config) func() {
	stop, _ := startApplication(t, binary, dsn, config)
	return stop
}
func startApplication(t *testing.T, binary, dsn string, config Config, settings ...string) (func(), string) {
	stop, address, _ := startBoth(t, binary, dsn, config, settings...)
	return stop, address
}
func startBoth(t testing.TB, binary, dsn string, config Config, settings ...string) (func(), string, string) {
	stop, httpAddress, grpcAddress, _ := startBothManaged(t, binary, dsn, config, settings...)
	return stop, httpAddress, grpcAddress
}
func startBothManaged(t testing.TB, binary, dsn string, config Config, settings ...string) (func(), string, string, func() string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary)
	command.Env = append(os.Environ(),
		"DATABASE_URL="+dsn, "PORT=0",
		"STEGO_KAFKA_BROKERS="+strings.Join(config.Brokers, ","), "STEGO_KAFKA_TOPIC="+config.Topic,
		"STEGO_KAFKA_AUTHENTICATION="+config.Authentication, "STEGO_KAFKA_CA_FILE="+config.CAFile,
		"STEGO_KAFKA_CLIENT_CERTIFICATE_FILE="+config.ClientCertificateFile, "STEGO_KAFKA_CLIENT_KEY_FILE="+config.ClientKeyFile,
	)
	_, authSettings := issuer(t)
	command.Env = append(command.Env, authSettings...)
	tlsIdentity := identity(t, "localhost")
	command.Env = append(command.Env, "STEGO_GRPC_ADDR=127.0.0.1:0", "STEGO_GRPC_TLS_CERT="+filepath.Join(filepath.Dir(tlsIdentity.config.CAFile), "server.pem"), "STEGO_GRPC_TLS_KEY="+filepath.Join(filepath.Dir(tlsIdentity.config.CAFile), "server-key.pem"))
	command.Env = append(command.Env, settings...)
	if raceEnabled {
		command.Env = append(command.Env, "GORACE=halt_on_error=1 exitcode=66")
	}
	output := runtimeOutput{ready: make(chan string, 1), grpcReady: make(chan string, 1)}
	command.Stdout = &output
	command.Stderr = &output
	if err := command.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		defer cancel()
		if err := command.Process.Signal(syscall.SIGTERM); err != nil {
			t.Errorf("signal runtime: %v", err)
		}
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("runtime exit: %v\n%s", err, output.String())
			}
		case <-time.After(12 * time.Second):
			cancel()
			<-done
			t.Errorf("runtime did not stop\n%s", output.String())
		}
	}
	t.Cleanup(stop)
	var httpAddress, grpcAddress string
	timer := time.NewTimer(8 * time.Second)
	defer timer.Stop()
	for httpAddress == "" || grpcAddress == "" {
		select {
		case httpAddress = <-output.ready:
		case grpcAddress = <-output.grpcReady:
		case err := <-done:
			stopped = true
			cancel()
			t.Fatalf("runtime did not start: %v\n%s", err, output.String())
		case <-timer.C:
			stop()
			t.Fatalf("runtime did not report its listeners\n%s", output.String())
		}
	}
	waitFailure := func() string {
		t.Helper()
		select {
		case err := <-done:
			stopped = true
			cancel()
			if err == nil {
				t.Fatal("runtime reported success after a source failure")
			}
			return output.String()
		case <-time.After(12 * time.Second):
			t.Fatal("runtime did not stop after a source failure")
			return ""
		}
	}
	return stop, httpAddress, grpcAddress, waitFailure
}
func readEvent(t *testing.T, consumer *kgo.Client, id string) string {
	return readGatewayEvent(t, consumer, id, "Create", "gateway.created")
}
func readGatewayEvent(t *testing.T, consumer *kgo.Client, id, eventType, kind string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		fetches := consumer.PollRecords(ctx, 1)
		for _, record := range fetches.Records() {
			if string(record.Key) != id {
				continue
			}
			var payload map[string]string
			if err := json.Unmarshal(record.Value, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 3 || payload["source"] != "Gateways" || payload["source_id"] != id || payload["event_type"] != eventType {
				t.Fatalf("wrong event payload: %s", record.Value)
			}
			headers := map[string]string{}
			for _, h := range record.Headers {
				headers[h.Key] = string(h.Value)
			}
			if headers["stego-message-kind"] != kind {
				t.Fatal("event kind was lost")
			}
			return headers["stego-message-id"]
		}
	}
	t.Fatalf("no event for Gateway %s: %v", id, ctx.Err())
	return ""
}
func awaitQueueEmpty(t testing.TB, f *fixture) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for count(t, f.db, "stego_outbox.messages") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("acknowledged event remains in the queue")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// runtimeOutput captures process output without racing with startup detection.
type runtimeOutput struct {
	mu        sync.Mutex
	data      bytes.Buffer
	ready     chan string
	grpcReady chan string
}

func (w *runtimeOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.data.Write(data)
	for _, line := range strings.Split(w.data.String(), "\n") {
		if _, value, found := strings.Cut(line, "gRPC server started address="); found {
			address := strings.Trim(strings.TrimSpace(value), "\"")
			if _, _, err := net.SplitHostPort(address); err == nil {
				select {
				case w.grpcReady <- address:
				default:
				}
			}
		}
		_, value, found := strings.Cut(line, "starting server on ")
		if !found {
			continue
		}
		_, port, err := net.SplitHostPort(strings.TrimSpace(value))
		if err != nil {
			continue
		}
		select {
		case w.ready <- "http://127.0.0.1:" + port:
		default:
		}
	}
	return n, err
}
func (w *runtimeOutput) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.data.String() }
