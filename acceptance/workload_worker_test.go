package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkloadapp"
	"github.com/segmentio/ksuid"
	logpb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

func TestGeneratedWorkloadWorkerStartupPrivacy(t *testing.T) {
	for _, name := range []string{"namespace-allocation", "gateway-identity", "gateway-workload", "sandbox-count"} {
		t.Run(name, func(t *testing.T) {
			binary := buildProgram(t, "./out/deploy/workers/"+name)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			monitor := listener.Addr().String()
			if err := listener.Close(); err != nil {
				t.Fatal(err)
			}
			command := exec.CommandContext(ctx, binary)
			cluster := ksuid.New().String()
			image := "example.invalid/fixture@sha256:" + strings.Repeat("0", 64)
			apiCA, apiToken := "/private-worker-trust", "/private-worker-token"
			if name == "gateway-workload" {
				apiCA = identity(t, "localhost").config.CAFile
				apiToken = filepath.Join(t.TempDir(), "api-token")
				if err := os.WriteFile(apiToken, []byte("private-worker-token"), 0600); err != nil {
					t.Fatal(err)
				}
			}

			settings := []string{
				"STEGO_CONTROLLER_MONITOR_ADDR=" + monitor,
				"OTEL_EXPORTER_OTLP_ENDPOINT=",
				"HYPERSHELL_API_GRPC_ADDR=localhost:443",
				"HYPERSHELL_API_CA_FILE=" + apiCA,
				"HYPERSHELL_API_TOKEN_FILE=" + apiToken,
				"HYPERSHELL_CONTROL_NAMESPACE=fixture",
				"HYPERSHELL_GATEWAY_DATABASE_CONFIG_FILE=" + filepath.Join(t.TempDir(), "database.json"),
				"HYPERSHELL_MANAGED_CLUSTER_ID=" + cluster,
				"HYPERSHELL_GATEWAY_CLUSTER_ISSUER=fixture",
				"HYPERSHELL_GATEWAY_OIDC_ISSUER=https://issuer.invalid",
				"HYPERSHELL_GATEWAY_SANDBOX_IMAGE=" + image,
				"HYPERSHELL_GATEWAY_SUPERVISOR_IMAGE=" + image,
				"HYPERSHELL_GATEWAY_SANDBOX_RUNTIME_CLASS=",
				"HYPERSHELL_GATEWAY_TRUST_BUNDLE=/private-worker-trust",
				"HYPERSHELL_KUBERNETES_URL=http://private-worker-provider.invalid/private?token=private-worker-startup",
				"HYPERSHELL_KUBERNETES_TOKEN_FILE=/private-worker-token",
				"HYPERSHELL_KUBERNETES_CA_FILE=/private-worker-ca"}
			for _, setting := range settings {
				name, value, _ := strings.Cut(setting, "=")
				t.Setenv(name, value)
			}
			service := "hypershell-startup-" + name
			collector, telemetrySettings := newHTTPDiagnosticCollector(t)
			for _, setting := range append(telemetrySettings, "OTEL_SERVICE_NAME="+service, "OTEL_LOGS_EXPORTER=otlp") {
				key, value, _ := strings.Cut(setting, "=")
				t.Setenv(key, value)
			}
			t.Setenv("HYPERSHELL_KEYCLOAK_URL", "http://private-worker-identity.invalid")
			if name == "gateway-workload" {
				err := gatewayworkloadapp.Run(ctx, nil)
				if err == nil || !strings.Contains(err.Error(), "/private-worker-trust") {
					t.Fatal("startup fixture did not reach the private provider error")
				}
			}
			command.Env = os.Environ()
			output, err := command.CombinedOutput()
			var exit *exec.ExitError
			if ctx.Err() != nil || !errors.As(err, &exit) || exit.ExitCode() != 1 {
				t.Fatalf("worker startup exit: %v", err)
			}
			if strings.Contains(string(output), "private-worker") || strings.Contains(string(output), "goroutine ") {
				t.Fatal("worker exposed private startup data")
			}
			failures := 0
			instance := ""
			localEvents := map[string]int{}
			for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
				var event map[string]any
				if json.Unmarshal([]byte(line), &event) != nil {
					t.Fatal("worker emitted an unstructured startup record")
				}
				if event["event.name"] == "controller.process.failed" {
					failures++
					if len(event) != 3 || event["severity"] != "ERROR" || event["message"] != "Controller process failed" {
						t.Fatal("worker changed the fixed failure record")
					}
					continue
				}
				id, ok := event["service.instance.id"].(string)
				if !ok || !telemetryInstancePattern.MatchString(id) || (instance != "" && instance != id) || event["service.name"] != service || len(event) != 6 {
					t.Fatal("startup telemetry has invalid service identity or fields")
				}
				instance = id
				eventName, _ := event["event.name"].(string)
				message, severity := workerStartupEvent(eventName)
				if message == "" || event["message"] != message || event["severity"] != severity {
					t.Fatal("invalid startup lifecycle event")
				}
				if stamp, ok := event["timestamp"].(string); !ok {
					t.Fatal("missing startup timestamp")
				} else if _, err := time.Parse(time.RFC3339Nano, stamp); err != nil {
					t.Fatal("invalid startup timestamp")
				}
				localEvents[eventName]++
			}
			if failures != 1 {
				t.Fatalf("worker failure records: got %d, want 1", failures)
			}
			checkWorkerStartupExport(t, collector, service, instance, localEvents)
		})
	}
}

func workerStartupEvent(event string) (string, string) {
	switch event {
	case "telemetry.runtime.started":
		return "Telemetry runtime started", "INFO"
	case "telemetry.runtime.stopped":
		return "Telemetry runtime stopped", "INFO"
	case "service.failed":
		return "Service failed", "ERROR"
	}
	return "", ""
}

func checkWorkerStartupExport(t *testing.T, collector *httpDiagnosticCollector, service, instance string, local map[string]int) {
	t.Helper()
	remote := map[string]int{}
	// The child has completed its bounded flush. Export handlers store each batch
	// before acknowledgment, so no additional wait can turn missing output into a pass.
drain:
	for {
		select {
		case batch := <-collector.logs.received:
			encoded, err := proto.Marshal(batch)
			if err != nil || strings.Contains(string(encoded), "private-worker") {
				t.Fatal("invalid or private startup export")
			}
			for _, resource := range batch.ResourceLogs {
				if signalAttribute(resource.Resource.GetAttributes(), "service.name").GetStringValue() != service || signalAttribute(resource.Resource.GetAttributes(), "service.instance.id").GetStringValue() != instance {
					t.Fatal("exported startup identity differs from local output")
				}
				for _, scope := range resource.ScopeLogs {
					if scope.Scope.Name != "stego/service" {
						t.Fatal("unexpected startup log scope")
					}
					for _, record := range scope.LogRecords {
						message, severity := workerStartupEvent(record.EventName)
						number := logpb.SeverityNumber_SEVERITY_NUMBER_INFO
						if severity == "ERROR" {
							number = logpb.SeverityNumber_SEVERITY_NUMBER_ERROR
						}
						if message == "" || record.Body.GetStringValue() != message || record.SeverityText != severity || record.SeverityNumber != number || len(record.Attributes) != 0 || len(record.TraceId) != 0 || len(record.SpanId) != 0 || record.TimeUnixNano == 0 {
							t.Fatal("invalid exported startup event")
						}
						remote[record.EventName]++
					}
				}
			}
		default:
			break drain
		}
	}
	for _, name := range []string{"telemetry.runtime.started", "service.failed", "telemetry.runtime.stopped"} {
		if local[name] != 1 || remote[name] != 1 {
			t.Fatal("missing or repeated startup event", name, local[name], remote[name])
		}
	}
	t.Log("Generated worker exported its setup failure and runtime lifecycle with the same local and OTLP identity")
}
