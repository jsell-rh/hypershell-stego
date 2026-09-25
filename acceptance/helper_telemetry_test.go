package acceptance

import (
	"context"
	"crypto/tls"
	"errors"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	logcollector "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metriccollector "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	tracecollector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// helperTelemetryCollector starts one TLS OTLP collector and points the process
// telemetry at it. Set the environment before the first controller call. The
// tests must not run in parallel. They share one telemetry owner per process.
func helperTelemetryCollector(t *testing.T) <-chan *logcollector.ExportLogsServiceRequest {
	t.Helper()
	cert := identity(t, "localhost")
	directory := filepath.Dir(cert.config.CAFile)
	pair, err := tls.LoadX509KeyPair(filepath.Join(directory, "server.pem"), filepath.Join(directory, "server-key.pem"))
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	collector := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS13, Certificates: []tls.Certificate{pair}})))
	traces := &workflowTraceCollector{received: make(chan *tracecollector.ExportTraceServiceRequest, 64)}
	logs := &workflowLogCollector{received: make(chan *logcollector.ExportLogsServiceRequest, 64)}
	metrics := &workflowMetricCollector{received: make(chan *metriccollector.ExportMetricsServiceRequest, 64)}
	tracecollector.RegisterTraceServiceServer(collector, traces)
	logcollector.RegisterLogsServiceServer(collector, logs)
	metriccollector.RegisterMetricsServiceServer(collector, metrics)
	go collector.Serve(listener)
	t.Cleanup(func() { collector.Stop() })
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "https://"+listener.Addr().String())
	t.Setenv("OTEL_EXPORTER_OTLP_CERTIFICATE", cert.config.CAFile)
	t.Setenv("OTEL_SERVICE_NAME", "hypershell-accounts")
	t.Setenv("OTEL_TRACES_SAMPLER_ARG", "1")
	t.Setenv("OTEL_METRIC_EXPORT_INTERVAL", "1000")
	return logs.received
}

// helperTelemetryFixture makes one deleted gateway with 101 journal rows. The
// account cleanup needs two passes. The first pass reads one page. The second
// pass reads the tail and checks the provider inventory.
func helperTelemetryFixture(t *testing.T) (*fixture, *serviceaccounts.Service, *rescanCleanupProvider, model.Gateway, string) {
	t.Helper()
	f := database(t)
	ctx := context.Background()
	original := newAccountProvider()
	_, gateway := accountService(t, f, original)
	scope := gateways.AccountProviderStateScope(gateway.ID)
	for i := 0; i < 101; i++ {
		id := ksuid.New().String()
		if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
			_, err := tx.(storage.ResourceStateStore).SaveResourceState(ctx, "ServiceAccount", id, scope, 0, nil)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.storage.Delete(ctx, "Gateway", gateway.ID); err != nil {
		t.Fatal(err)
	}
	provider := &rescanCleanupProvider{boundedCleanupProvider: &boundedCleanupProvider{accountProvider: original, confirmed: map[string]int{}}}
	accounts, err := serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	return f, accounts, provider, gateway, scope
}

type helperWorkRecord struct {
	operation string
	outcome   string
	retry     bool
}

// awaitHelperTelemetry collects controller work records until the check passes.
// The controller closes its telemetry before Monitor or Run returns. All
// records are in the channel when this function starts.
func awaitHelperTelemetry(t *testing.T, logs <-chan *logcollector.ExportLogsServiceRequest, check func([]helperWorkRecord) bool) []helperWorkRecord {
	t.Helper()
	deadline := time.After(8 * time.Second)
	records := []helperWorkRecord{}
	for {
		if check(records) {
			return records
		}
		select {
		case batch := <-logs:
			for _, resource := range batch.ResourceLogs {
				for _, scope := range resource.ScopeLogs {
					for _, record := range scope.LogRecords {
						if record.EventName != "controller.work.completed" {
							continue
						}
						records = append(records, helperWorkRecord{
							operation: signalAttribute(record.Attributes, "operation").GetStringValue(),
							outcome:   signalAttribute(record.Attributes, "outcome").GetStringValue(),
							retry:     signalAttribute(record.Attributes, "retry").GetBoolValue(),
						})
					}
				}
			}
		case <-deadline:
			t.Fatal("controller work records did not arrive", records)
		}
	}
}

func countHelperRecords(records []helperWorkRecord, operation, outcome string, retry bool) int {
	total := 0
	for _, record := range records {
		if record.operation == operation && record.outcome == outcome && record.retry == retry {
			total++
		}
	}
	return total
}

// A helper called outside a controller operation records its own work. The
// delegation chain from the scan cycle through the observation and the
// inventory cycle records one scan per outermost pass, not one per helper.
func TestHelperTelemetryRecordsStandaloneCleanup(t *testing.T) {
	logs := helperTelemetryCollector(t)
	f, accounts, provider, gateway, scope := helperTelemetryFixture(t)
	var passes []bool
	err := runtime.Monitor(context.Background(), "", func(ctx context.Context, _ *runtime.Metrics) error {
		for pass := 0; pass < 2; pass++ {
			complete, err := accounts.RecoverGatewayCleanup(ctx, gateway.ID)
			if err != nil {
				return err
			}
			passes = append(passes, complete)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(passes) != 2 || passes[0] || !passes[1] {
		t.Fatal("standalone cleanup did not cross its page boundary", passes)
	}
	if provider.calls != 101 || provider.inventory != 1 {
		t.Fatal("standalone cleanup proof is absent", provider.calls, provider.inventory)
	}
	membership, err := f.storage.LoadResourceStateScope(context.Background(), "ServiceAccount", scope)
	if err != nil || !membership.Sealed {
		t.Fatal("standalone cleanup did not seal the account scope", membership, err)
	}
	records := awaitHelperTelemetry(t, logs, func(records []helperWorkRecord) bool {
		return len(records) >= 3
	})
	// Two passes give three scan records. The first pass runs one cycle. The
	// second pass runs the account cycle and the inventory cycle.
	if len(records) != 3 || countHelperRecords(records, "scan", "success", false) != 3 {
		t.Fatal("standalone cleanup did not record one scan per pass", records)
	}
}

// A helper failure outside a controller operation records a failure outcome.
// The record keeps the failure without the error text.
func TestHelperTelemetryRecordsCleanupFailureOutcome(t *testing.T) {
	logs := helperTelemetryCollector(t)
	_, accounts, provider, gateway, _ := helperTelemetryFixture(t)
	provider.failure = errors.New("private provider failure")
	var failures int
	err := runtime.Monitor(context.Background(), "", func(ctx context.Context, _ *runtime.Metrics) error {
		for pass := 0; pass < 2; pass++ {
			complete, err := accounts.RecoverGatewayCleanup(ctx, gateway.ID)
			if complete || !errors.Is(err, provider.failure) {
				return err
			}
			failures++
		}
		return nil
	})
	if err != nil || failures != 2 {
		t.Fatal("standalone cleanup did not fail each pass", err, failures)
	}
	records := awaitHelperTelemetry(t, logs, func(records []helperWorkRecord) bool {
		return len(records) >= 2
	})
	if len(records) != 2 || countHelperRecords(records, "scan", "failure", false) != 2 {
		t.Fatal("standalone cleanup failure was not recorded", records)
	}
}

// A helper called inside the sweep records nothing. The sweep page reads and
// the sweep reconciles are the only recorded operations. The sweep has ten
// groups with thirteen streams. Each round records one scan per stream. The
// cleanup needs two rounds. The first reconcile returns busy. The second
// reconcile completes the cleanup. A nested scan record would raise the count
// above the twenty-six page reads.
func TestHelperTelemetrySkipsCleanupInsideSweep(t *testing.T) {
	logs := helperTelemetryCollector(t)
	f, accounts, provider, _, scope := helperTelemetryFixture(t)
	if provider.calls != 0 {
		t.Fatal("fixture must start without provider calls")
	}
	work, cancelWork := context.WithCancel(context.Background())
	defer cancelWork()
	done := make(chan error, 1)
	go func() { done <- accounts.Run(work) }()
	// Wait for the durable proof. The seal commits inside the second round
	// reconcile. The sweep then waits one second before the next round.
	sealed := false
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		membership, err := f.storage.LoadResourceStateScope(context.Background(), "ServiceAccount", scope)
		if err != nil {
			t.Fatal(err)
		}
		if membership.Sealed {
			sealed = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !sealed {
		t.Fatal("the sweep did not complete the gateway cleanup")
	}
	cancelWork()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if provider.calls != 101 || provider.inventory != 1 {
		t.Fatal("sweep cleanup proof is absent", provider.calls, provider.inventory)
	}
	records := awaitHelperTelemetry(t, logs, func(records []helperWorkRecord) bool {
		scans, reconciles := 0, 0
		for _, record := range records {
			if record.operation == "scan" {
				scans++
			}
			if record.operation == "reconcile" {
				reconciles++
			}
		}
		return scans >= 26 && reconciles >= 2
	})
	if len(records) != 28 {
		t.Fatal("the sweep recorded unexpected work", records)
	}
	if countHelperRecords(records, "scan", "success", false) != 26 {
		t.Fatal("the sweep did not record one scan per stream page read", records)
	}
	if countHelperRecords(records, "reconcile", "failure", true) != 1 || countHelperRecords(records, "reconcile", "success", false) != 1 {
		t.Fatal("the sweep did not record the busy retry and the completed cleanup", records)
	}
}
