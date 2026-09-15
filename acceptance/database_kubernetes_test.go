package acceptance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/google/uuid"
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
func kubernetesFixture(t *testing.T) *kubeFixture {
	t.Helper()
	config := os.Getenv("STEGO_TEST_KUBECONFIG")
	if config == "" {
		if os.Getenv("STEGO_REQUIRE_KUBERNETES") == "1" {
			t.Fatal("Kubernetes is required")
		}
		t.Skip("set STEGO_TEST_KUBECONFIG to an isolated kind cluster with cert-manager")
	}
	k := &kubeFixture{config: config}
	contextName := os.Getenv("STEGO_TEST_CONTEXT")
	if contextName == "" {
		t.Fatal("Kubernetes tests require an explicit context")
	}
	k.context = contextName
	if !strings.HasPrefix(contextName, "kind-stego-") {
		t.Fatal("database test requires a kind-stego- context")
	}
	name := "stego-db-" + uuid.NewString()[:8]
	meta := func(n string) map[string]any { return map[string]any{"name": n} }
	k.apply(t, map[string]any{"apiVersion": "v1", "kind": "Namespace", "metadata": meta(name)}, map[string]any{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": map[string]any{"name": name, "namespace": name}},
		map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": meta(name), "rules": []any{
			map[string]any{"apiGroups": []string{""}, "resources": []string{"namespaces", "secrets", "configmaps", "persistentvolumeclaims", "services"}, "verbs": []string{"get", "create", "patch", "delete"}},
			map[string]any{"apiGroups": []string{""}, "resources": []string{"pods"}, "verbs": []string{"get"}},
			map[string]any{"apiGroups": []string{"postgresql.cnpg.io"}, "resources": []string{"clusters"}, "verbs": []string{"get", "create", "patch", "delete"}},
			map[string]any{"apiGroups": []string{"apps"}, "resources": []string{"deployments"}, "verbs": []string{"get", "create", "patch"}},
			map[string]any{"apiGroups": []string{"cert-manager.io"}, "resources": []string{"certificates"}, "verbs": []string{"get", "create", "patch"}},
		}}, map[string]any{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRoleBinding", "metadata": meta(name), "roleRef": map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": name}, "subjects": []any{map[string]any{"kind": "ServiceAccount", "name": name, "namespace": name}}},
		map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer", "metadata": meta(name + "-self"), "spec": map[string]any{"selfSigned": map[string]any{}}})
	t.Cleanup(func() {
		k.must(t, "", "delete", "clusterrole,clusterrolebinding,clusterissuer", name, "--ignore-not-found=true")
		k.must(t, "", "delete", "clusterissuer", name+"-self", "--ignore-not-found=true")
		k.must(t, "", "-n", "cert-manager", "delete", "certificate,secret", name, "--ignore-not-found=true")
		k.must(t, "", "delete", "namespace", name, "--wait=false", "--ignore-not-found=true")
	})
	k.apply(t, map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "Certificate", "metadata": map[string]any{"name": name, "namespace": "cert-manager"}, "spec": map[string]any{"isCA": true, "commonName": "STEGO isolated database test CA", "secretName": name, "issuerRef": map[string]any{"name": name + "-self", "kind": "ClusterIssuer"}, "privateKey": map[string]any{"algorithm": "ECDSA", "size": 256}}})
	k.must(t, "", "-n", "cert-manager", "wait", "certificate/"+name, "--for=condition=Ready", "--timeout=60s")
	k.apply(t, map[string]any{"apiVersion": "cert-manager.io/v1", "kind": "ClusterIssuer", "metadata": meta(name), "spec": map[string]any{"ca": map[string]any{"secretName": name}}})
	directory := t.TempDir()
	caEncoded := strings.TrimSpace(string(k.must(t, "", "config", "view", "--raw", "--minify", "-o", "jsonpath={.clusters[0].cluster.certificate-authority-data}")))
	ca, err := base64.StdEncoding.DecodeString(caEncoded)
	if err != nil {
		t.Fatal(err)
	}
	token := k.must(t, "", "-n", name, "create", "token", name, "--duration=1h")
	caPath, tokenPath := filepath.Join(directory, "ca.pem"), filepath.Join(directory, "token")
	if err := os.WriteFile(caPath, ca, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tokenPath, token, 0600); err != nil {
		t.Fatal(err)
	}
	address := strings.TrimSpace(string(k.must(t, "", "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")))
	k.options = gatewayworkload.Options{ServerURL: address, CAFile: caPath, TokenFile: tokenPath, ClusterIssuer: name}
	return k
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
