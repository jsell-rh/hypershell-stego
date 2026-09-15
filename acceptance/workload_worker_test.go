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
)

func TestGeneratedWorkloadWorkerStartupPrivacy(t *testing.T) {
	for _, name := range []string{"namespace-allocation", "gateway-workload", "sandbox-count"} {
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
				}
			}
			if failures != 1 {
				t.Fatalf("worker failure records: got %d, want 1", failures)
			}
		})
	}
}
