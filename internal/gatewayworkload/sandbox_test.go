package gatewayworkload

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func TestSandboxSetupUsesBoundedDryRuns(t *testing.T) {
	for _, denied := range []int{http.StatusForbidden, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(denied), func(t *testing.T) {
			requests := 0
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.Method != http.MethodPost || r.URL.Path != "/api/v1/namespaces/sandbox/pods" || (r.URL.Query().Get("dryRun") != "All" || r.URL.Query().Get("fieldValidation") != "Strict" || len(r.URL.Query()) != 2) {
					t.Fatalf("unexpected Sandbox write: %s %s", r.Method, r.URL)
				}
				var pod object
				if err := json.NewDecoder(r.Body).Decode(&pod); err != nil {
					t.Fatal(err)
				}
				if requests == 1 {
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(pod)
					return
				}
				if _, present := pod["spec"].(map[string]any)["runtimeClassName"]; present {
					t.Fatal("negative probe retained runtime")
				}
				w.WriteHeader(denied)
			})
			k.options.SandboxRuntimeClass = "kata"
			if err := k.checkSandboxAdmission(context.Background(), "owner", "sandbox", "allocated-sandbox-account"); err != nil {
				t.Fatal(err)
			}
			if requests != 2 {
				t.Fatal("incomplete admission check", requests)
			}
		})
	}
}

func TestSandboxSetupRejectsChangedOrMissingGuards(t *testing.T) {
	for _, mode := range []string{"runtime", "account", "workspace", "socket", "credential", "exposed credential", "secret environment", "secret environment source", "allowed negative", "server error"} {
		t.Run(mode, func(t *testing.T) {
			requests := 0
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				requests++
				if r.URL.Query().Get("dryRun") != "All" || r.URL.Query().Get("fieldValidation") != "Strict" || len(r.URL.Query()) != 2 {
					t.Fatal("Pod persistence was requested")
				}
				var pod object
				if err := json.NewDecoder(r.Body).Decode(&pod); err != nil {
					t.Fatal(err)
				}
				spec := pod["spec"].(map[string]any)
				if requests == 1 {
					switch mode {
					case "runtime":
						spec["runtimeClassName"] = "other"
					case "account":
						spec["serviceAccountName"] = "default"
					case "workspace":
						spec["initContainers"].([]any)[0].(map[string]any)["securityContext"].(map[string]any)["runAsUser"] = float64(1000)
					case "socket":
						spec["volumes"].([]any)[0].(map[string]any)["emptyDir"] = map[string]any{"medium": "Memory"}
					case "exposed credential":
						spec["containers"].([]any)[0].(map[string]any)["volumeMounts"] = []any{map[string]any{"name": "openshell-client-tls", "mountPath": "/identity"}}
					case "credential":
						spec["containers"].([]any)[1].(map[string]any)["volumeMounts"] = []any{}
					case "secret environment":
						spec["containers"].([]any)[0].(map[string]any)["env"] = []any{map[string]any{"name": "KEY", "valueFrom": map[string]any{"secretKeyRef": map[string]any{"name": "openshell-client-tls", "key": "tls.key"}}}}
					case "secret environment source":
						spec["initContainers"].([]any)[0].(map[string]any)["envFrom"] = []any{map[string]any{"secretRef": map[string]any{"name": "openshell-client-tls"}}}
					}
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(pod)
					return
				}
				if mode == "allowed negative" {
					w.WriteHeader(http.StatusCreated)
					_ = json.NewEncoder(w).Encode(pod)
				} else {
					w.WriteHeader(http.StatusInternalServerError)
				}
			})
			k.options.SandboxRuntimeClass = "kata"
			if err := k.checkSandboxAdmission(context.Background(), "owner", "sandbox", "account"); err == nil {
				t.Fatal("changed or missing guard accepted")
			}
		})
	}
}

func TestSandboxProbeRetainsUpstreamSetup(t *testing.T) {
	pod := sandboxProbe("owner", "sandbox", "allocated-account", Options{SandboxRuntimeClass: "kata", SandboxImage: "workload", SupervisorImage: "supervisor"})
	spec := pod["spec"].(object)
	if spec["serviceAccountName"] != "allocated-account" || spec["automountServiceAccountToken"] != false {
		t.Fatal("allocated account was lost")
	}
	init := spec["initContainers"].([]object)
	if init[0]["securityContext"].(object)["runAsUser"] != 0 {
		t.Fatal("workspace user was changed")
	}
	if len(spec["volumes"].([]object)[0]["emptyDir"].(object)) != 0 {
		t.Fatal("socket volume was changed")
	}
	all := append(append([]object{}, spec["containers"].([]object)...), init...)
	for _, container := range all {
		if container["securityContext"].(object)["privileged"] == true {
			t.Fatal("privileged probe")
		}
		if len(container["resources"].(object)["limits"].(object)) != 3 {
			t.Fatal("probe has no resource bound")
		}
		if container["name"] == "agent" || container["name"] == "workspace-init" {
			if _, exists := container["volumeMounts"]; exists {
				t.Fatal("workload received client credentials")
			}
		}
	}
	config := configuration("gateway", "sandbox", "allocated-account", Options{})
	if !strings.Contains(config, "service_account_name = \"allocated-account\"") {
		t.Fatal("Gateway ignored the allocated Sandbox account")
	}
	if !kube.Contains(pod, pod) {
		t.Fatal("invalid probe object")
	}
}

func TestSandboxWithoutAllocationCannotReachKubernetes(t *testing.T) {
	k := fixture(t, func(http.ResponseWriter, *http.Request) { t.Fatal("unallocated Sandbox reached Kubernetes") })
	k.allocation = nil
	if _, err := k.ensureSandbox(context.Background(), "owner", "sandbox", "gateway", nil); err == nil {
		t.Fatal("Sandbox accepted without allocation")
	}
}
