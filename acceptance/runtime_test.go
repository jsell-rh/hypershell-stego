package acceptance

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
	binary := filepath.Join(t.TempDir(), "hypershell-events")
	build := exec.Command("go", "build", "-mod=readonly", "-o", binary, "./out")
	build.Dir = ".."
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build generated runtime: %v\n%s", err, output)
	}
	ca, err := os.ReadFile(config.CAFile)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		t.Fatal("invalid test CA")
	}
	pair, err := tls.LoadX509KeyPair(config.ClientCertificateFile, config.ClientKeyFile)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := kgo.NewClient(kgo.SeedBrokers(config.Brokers...), kgo.DialTLSConfig(&tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{pair}}), kgo.ConsumeTopics(config.Topic), kgo.ConsumeResetOffset(kgo.NewOffset().AtStart()))
	if err != nil {
		t.Fatal(err)
	}
	defer consumer.Close()
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
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	command := exec.CommandContext(ctx, binary)
	command.Env = append(os.Environ(),
		"DATABASE_URL="+dsn,
		"STEGO_KAFKA_BROKERS="+strings.Join(config.Brokers, ","), "STEGO_KAFKA_TOPIC="+config.Topic,
		"STEGO_KAFKA_AUTHENTICATION="+config.Authentication, "STEGO_KAFKA_CA_FILE="+config.CAFile,
		"STEGO_KAFKA_CLIENT_CERTIFICATE_FILE="+config.ClientCertificateFile, "STEGO_KAFKA_CLIENT_KEY_FILE="+config.ClientKeyFile,
	)
	var output bytes.Buffer
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
	return stop
}
func readEvent(t *testing.T, consumer *kgo.Client, id string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for ctx.Err() == nil {
		fetches := consumer.PollRecords(ctx, 10)
		for _, record := range fetches.Records() {
			if string(record.Key) != id {
				continue
			}
			var payload map[string]string
			if err := json.Unmarshal(record.Value, &payload); err != nil {
				t.Fatal(err)
			}
			if len(payload) != 3 || payload["source"] != "Gateways" || payload["source_id"] != id || payload["event_type"] != "Create" {
				t.Fatalf("wrong event payload: %s", record.Value)
			}
			headers := map[string]string{}
			for _, h := range record.Headers {
				headers[h.Key] = string(h.Value)
			}
			if headers["stego-message-kind"] != "gateway.created" {
				t.Fatal("event kind was lost")
			}
			return headers["stego-message-id"]
		}
	}
	t.Fatalf("no event for Gateway %s: %v", id, ctx.Err())
	return ""
}
func awaitQueueEmpty(t *testing.T, f *fixture) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for count(t, f.db, "stego_outbox.messages") != 0 {
		if time.Now().After(deadline) {
			t.Fatal("acknowledged event remains in the queue")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
