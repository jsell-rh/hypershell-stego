package acceptance

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
)

type kubeFixture struct {
	config  string
	context string
	options gatewayworkload.Options
}

func (k *kubeFixture) command(ctx context.Context, input string, args ...string) ([]byte, error) {
	flags := []string{"--kubeconfig", k.config}
	if k.context != "" {
		flags = append(flags, "--context", k.context)
	}
	cmd := exec.CommandContext(ctx, "kubectl", append(flags, args...)...)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	return cmd.CombinedOutput()
}
func (k *kubeFixture) must(t *testing.T, input string, args ...string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	output, err := k.command(ctx, input, args...)
	if err != nil {
		t.Fatalf("Kubernetes %s: %v\n%s", args[0], err, output)
	}
	return output
}
func (k *kubeFixture) apply(t *testing.T, objects ...map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"apiVersion": "v1", "kind": "List", "items": objects})
	if err != nil {
		t.Fatal(err)
	}
	k.must(t, string(body), "apply", "-f", "-")
}
func startDatabaseController(t *testing.T, binary string, k *kubeFixture, address, ca, bearer string, settings ...string) (func(), func() string) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	monitor := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(file, []byte(bearer), 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(binary)
	cmd.Env = append(os.Environ(), "STEGO_CONTROLLER_MONITOR_ADDR="+monitor, "HYPERSHELL_API_GRPC_ADDR="+address, "HYPERSHELL_API_CA_FILE="+ca, "HYPERSHELL_API_TOKEN_FILE="+file,
		"HYPERSHELL_KUBERNETES_URL="+k.options.ServerURL, "HYPERSHELL_KUBERNETES_CA_FILE="+k.options.CAFile, "HYPERSHELL_KUBERNETES_TOKEN_FILE="+k.options.TokenFile)
	cmd.Env = append(cmd.Env, settings...)
	if raceEnabled {
		cmd.Env = append(cmd.Env, "GORACE=halt_on_error=1 exitcode=66")
	}
	output := &runtimeOutput{}
	cmd.Stdout = output
	cmd.Stderr = output
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		_ = cmd.Process.Signal(syscall.SIGTERM)
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("worker exit: %v\n%s", err, output.String())
			}
		case <-time.After(8 * time.Second):
			_ = cmd.Process.Kill()
			<-done
			t.Errorf("worker did not stop\n%s", output.String())
		}
	}
	t.Cleanup(stop)
	for _, mode := range []string{"live", "ready"} {
		deadline := time.Now().Add(15 * time.Second)
		for {
			select {
			case err := <-done:
				stopped = true
				t.Fatalf("generated worker stopped before its %s probe: %v\n%s", mode, err, output.String())
			default:
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			probe := exec.CommandContext(ctx, binary, "--stego-probe="+mode)
			probe.Env = cmd.Env
			err := probe.Run()
			cancel()
			if err == nil {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("generated worker %s probe did not pass\n%s", mode, output.String())
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	return stop, output.String
}

// Common controller logs contain fixed outcomes, not provider error text.
func controllerRetryLogged(output string) bool {
	for _, line := range strings.Split(output, "\n") {
		var record struct {
			Event     string `json:"event.name"`
			Operation string `json:"operation"`
			Outcome   string `json:"outcome"`
			Retry     bool   `json:"retry"`
		}
		if json.Unmarshal([]byte(line), &record) == nil && record.Event == "controller.work.completed" && record.Operation == "reconcile" && record.Outcome == "failure" && record.Retry {
			return true
		}
	}
	return false
}
