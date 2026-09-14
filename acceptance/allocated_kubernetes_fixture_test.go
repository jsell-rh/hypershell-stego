package acceptance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// Run workers as separate processes with their generated Kubernetes roles.
// The admin connection is only for test setup, observation, and fault injection.
// This fixture does not test worker Deployments or NetworkPolicies.
func allocatedKubernetesFixture(t *testing.T, cluster string, workers ...string) map[string]*kubeFixture {
	t.Helper()
	if os.Getenv("STEGO_TEST_ALLOCATED_FIXTURE") == "1" {
		return suppliedAllocationFixture(t, cluster, workers...)
	}
	config := os.Getenv("STEGO_TEST_KUBECONFIG")
	if config == "" {
		if os.Getenv("STEGO_REQUIRE_KUBERNETES") == "1" {
			t.Fatal("Kubernetes is required")
		}
		t.Skip("requires an isolated kind-stego- cluster")
	}
	k := &kubeFixture{config: config}
	if name := strings.TrimSpace(string(k.must(t, "", "config", "current-context"))); !strings.HasPrefix(name, "kind-stego-") {
		t.Fatal("allocation test requires a kind-stego- context")
	}
	namespace := "stego-allocated-" + uuid.NewString()[:8]
	// Do not adopt an existing namespace.
	k.must(t, "", "create", "namespace", namespace)
	t.Cleanup(func() { k.must(t, "", "delete", "namespace", namespace, "--wait=true", "--timeout=60s") })
	directory := t.TempDir()
	caEncoded := strings.TrimSpace(string(k.must(t, "", "config", "view", "--raw", "--minify", "-o", "jsonpath={.clusters[0].cluster.certificate-authority-data}")))
	ca, err := base64.StdEncoding.DecodeString(caEncoded)
	if err != nil || len(ca) == 0 {
		t.Fatal("Kubernetes CA is missing or invalid")
	}
	caPath := filepath.Join(directory, "ca.pem")
	if err := os.WriteFile(caPath, ca, 0600); err != nil {
		t.Fatal(err)
	}
	server := strings.TrimSpace(string(k.must(t, "", "config", "view", "--minify", "-o", "jsonpath={.clusters[0].cluster.server}")))
	// The renderer needs a routable endpoint. Host processes use the kubeconfig
	// endpoint. No NetworkPolicy from this permission-only render is applied.
	ip := strings.TrimSpace(string(k.must(t, "", "-n", "default", "get", "service", "kubernetes", "-o", "jsonpath={.spec.clusterIP}")))
	renderer := buildProgram(t, "./out/deploy/render")
	image := "quay.io/opendatahub/odh-openshell-gateway@sha256:a80b79e514826e8d57ea137749cf18a6e7f3d92e26bfefe005f3a9c4a55b8bdd"
	result := map[string]*kubeFixture{}
	seen := map[string]bool{}
	for _, worker := range workers {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		data, err := exec.CommandContext(ctx, renderer, "--namespace", namespace, "--worker", worker, "--image", image, "--egress", "kubernetes="+net.JoinHostPort(ip, "443")).CombinedOutput()
		cancel()
		var document struct{ Items []map[string]any }
		if err != nil || json.Unmarshal(data, &document) != nil || len(document.Items) == 0 {
			t.Fatalf("render worker %s: %v\n%s", worker, err, data)
		}
		for _, item := range document.Items {
			kind, name, ns := kube.String(item, "kind"), kube.String(item, "metadata", "name"), kube.String(item, "metadata", "namespace")
			switch kind {
			case "ServiceAccount", "Role", "RoleBinding":
				if ns != namespace {
					t.Fatal("generated permission has a different namespace")
				}
			case "ClusterRole", "ClusterRoleBinding", "ValidatingAdmissionPolicy", "ValidatingAdmissionPolicyBinding":
				if ns != "" || !strings.HasPrefix(name, namespace+".") {
					t.Fatal("generated cluster permission has a different owner")
				}
			default:
				continue
			}
			key := kind + "/" + name
			if seen[key] {
				continue
			}
			seen[key] = true
			// Dynamic allocation cleanup is registered later and runs first.
			t.Cleanup(func() { k.must(t, "", "-n", namespace, "delete", key, "--ignore-not-found=true", "--timeout=30s") })
			k.apply(t, item)
		}
		token := k.must(t, "", "-n", namespace, "create", "token", "hypershell-"+worker, "--duration=20m")
		tokenPath := filepath.Join(directory, worker+".token")
		if err := os.WriteFile(tokenPath, token, 0600); err != nil {
			t.Fatal(err)
		}
		result[worker] = &kubeFixture{config: config, options: databasecontroller.KubernetesOptions{ServerURL: server, CAFile: caPath, TokenFile: tokenPath, ControlNamespace: namespace, ClusterID: cluster}}
	}
	return result
}

// The bounded cluster runner supplies generated roles and short-lived tokens.
// The driver token can inspect the test database and the named allocator role.
// It cannot change that role or read other application Secrets.
func suppliedAllocationFixture(t *testing.T, cluster string, workers ...string) map[string]*kubeFixture {
	t.Helper()
	namespace, contextName := os.Getenv("STEGO_TEST_NAMESPACE"), os.Getenv("STEGO_TEST_KUBERNETES_CONTEXT")
	k := &kubeFixture{config: os.Getenv("STEGO_TEST_KUBECONFIG"), context: contextName}
	if !strings.HasPrefix(namespace, "stego-cnpg-live-") || contextName == "" || k.config == "" {
		t.Fatal("bounded allocation fixture identity is missing")
	}
	if name := strings.TrimSpace(string(k.must(t, "", "config", "current-context"))); name != contextName {
		t.Fatal("bounded allocation fixture context differs")
	}
	result := map[string]*kubeFixture{}
	for _, worker := range workers {
		path := "/cnpg-credentials/" + worker
		if data, err := os.ReadFile(path); err != nil || len(strings.TrimSpace(string(data))) == 0 {
			t.Fatal("bounded worker credential is missing")
		}
		result[worker] = &kubeFixture{config: k.config, context: contextName, options: databasecontroller.KubernetesOptions{
			ServerURL: "https://kubernetes.default.svc", CAFile: "/cnpg-credentials/ca.crt", TokenFile: path,
			ControlNamespace: namespace, ClusterID: cluster,
		}}
	}
	return result
}

func prepareCNPGTestOperator(t *testing.T, namespace, id string) {
	t.Helper()
	if os.Getenv("STEGO_TEST_ALLOCATED_FIXTURE") != "1" {
		return
	}
	body, err := json.Marshal(map[string]string{"namespace": namespace, "id": id})
	if err != nil || os.WriteFile("/work/cnpg-request.tmp", body, 0600) != nil || os.Rename("/work/cnpg-request.tmp", "/work/cnpg-request.json") != nil {
		t.Fatal("cannot request the bounded CNPG operator")
	}
	until := time.Now().Add(150 * time.Second)
	for {
		if _, err := os.Stat("/work/operator-ready"); err == nil {
			return
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(until) {
			t.Fatal("bounded CNPG operator setup timed out")
		}
		time.Sleep(time.Second)
	}
}

func startCNPGTestOperator(t *testing.T, allocator *kubeFixture, namespace, id string) {
	t.Helper()
	if os.Getenv("STEGO_TEST_ALLOCATED_FIXTURE") != "1" {
		return
	}
	c := allocator.workerClient(t)
	defer c.Close()
	a, err := allocation.New(c, allocator.options.ControlNamespace)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for {
		err := a.RequireNamespace(ctx, "database", namespace, id)
		if err == nil {
			break
		}
		if !errors.Is(err, allocation.ErrPending) || ctx.Err() != nil {
			t.Fatal("CNPG operator namespace allocation", err)
		}
		time.Sleep(time.Second)
	}
	for {
		if _, err := os.Stat("/work/driver-ready"); err == nil {
			break
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if ctx.Err() != nil {
			t.Fatal("CNPG test driver setup timed out")
		}
		time.Sleep(time.Second)
	}
	allocator.must(t, "", "-n", "cnpg-system", "patch", "job", "cnpg-test-lifetime", "--type=merge", "-p", `{"spec":{"suspend":false}}`)
	allocator.must(t, "", "-n", "cnpg-system", "patch", "deployment", "cnpg-controller-manager", "--type=merge", "-p", `{"spec":{"replicas":1}}`)
}

// The host changes only namespace deletion in the frozen generated role. The
// test process has no permission to change or expand a ClusterRole.
func requestAllocatorDeleteAccess(t *testing.T, allow bool, sequence int) {
	t.Helper()
	request := struct {
		Sequence int  `json:"sequence"`
		Allow    bool `json:"allow_delete"`
	}{sequence, allow}
	data, err := json.Marshal(request)
	if err != nil || os.WriteFile("/work/allocator-access-request.tmp", data, 0600) != nil || os.Rename("/work/allocator-access-request.tmp", "/work/allocator-access-request.json") != nil {
		t.Fatal("cannot request allocator access change")
	}
	until := time.Now().Add(30 * time.Second)
	for {
		data, err := os.ReadFile("/work/allocator-access-ack.json")
		if err == nil {
			var ack struct {
				Sequence int  `json:"sequence"`
				Allow    bool `json:"allow_delete"`
			}
			if json.Unmarshal(data, &ack) != nil || ack.Sequence > sequence {
				t.Fatal("invalid allocator access acknowledgement")
			}
			if ack.Sequence == sequence {
				if ack.Allow != allow {
					t.Fatal("allocator access acknowledgement differs")
				}
				return
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if time.Now().After(until) {
			t.Fatal("allocator access change timed out")
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func (k *kubeFixture) workerClient(t *testing.T) *kube.Client {
	t.Helper()
	c, err := kube.New(kube.Options{ServerURL: k.options.ServerURL, CAFile: k.options.CAFile, TokenFile: k.options.TokenFile})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func (k *kubeFixture) cleanupAllocatedNamespace(t *testing.T, profile, namespace, id string) {
	t.Helper()
	t.Cleanup(func() {
		c := k.workerClient(t)
		defer c.Close()
		a, err := allocation.New(c, k.options.ControlNamespace)
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		for {
			gone, err := a.Delete(ctx, profile, namespace, id)
			if err != nil && !errors.Is(err, allocation.ErrPending) {
				t.Fatal("allocated namespace cleanup", err)
			}
			if gone {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal("allocated namespace cleanup timed out")
			case <-time.After(time.Second):
			}
		}
	})
}

func requireWorkerPermission(t *testing.T, client *kube.Client, verb, group, resource, namespace string, want bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	body := kube.Object{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "spec": kube.Object{"resourceAttributes": kube.Object{"verb": verb, "group": group, "resource": resource, "namespace": namespace}}}
	for {
		result, code, err := client.Request(ctx, http.MethodPost, "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", body)
		allowed, ok := kube.Nested(result, "status", "allowed").(bool)
		if err != nil || code != http.StatusCreated || !ok || kube.String(result, "status", "evaluationError") != "" {
			t.Fatalf("worker permission review failed: %d %v", code, err)
		}
		if allowed == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("worker permission %s %s/%s in %s did not become %t", verb, group, resource, namespace, want)
		case <-time.After(200 * time.Millisecond):
		}
	}
}
