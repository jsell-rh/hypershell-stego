package acceptance

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

const sandboxNetworkRuntimeClass = "stego-ci-sandbox-network"

// This fixture runs only fixed native packet probes. It cannot qualify Kata,
// OpenShell execution, or isolation from hostile Sandbox code.
func (w *browserGatewayWorkload) nativeSandboxNetworkEnabled() bool {
	w.t.Helper()
	f, err := os.Open("browser-inspection-source.json")
	if os.IsNotExist(err) {
		return false
	}
	if err != nil {
		w.t.Fatal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (1<<20)+1))
	var record struct {
		Native *struct {
			RuntimeClass string `json:"runtime_class"`
			Handler      string `json:"runtime_handler"`
			Production   string `json:"production_runtime_class"`
			VM           *bool  `json:"vm_isolation_tested"`
		} `json:"sandbox_network_probe"`
	}
	if err != nil || len(data) > 1<<20 || json.Unmarshal(data, &record) != nil {
		w.t.Fatal("invalid native network fixture record")
	}
	if record.Native == nil {
		return false
	}
	n := record.Native
	if n.RuntimeClass != sandboxNetworkRuntimeClass || n.Handler != "crun" || n.Production != "kata" || n.VM == nil || *n.VM {
		w.t.Fatal("native network fixture scope differs")
	}
	return true
}

func boundedNetworkPod(namespace, name, id, account, profile string, gatewayLabel bool, command []string, seconds int) kube.Object {
	labels := kube.Object{"stego.test/network-probe": id}
	container := "probe"
	security := kube.Object{"runAsNonRoot": true, "runAsUser": 1000, "runAsGroup": 1000, "readOnlyRootFilesystem": true, "allowPrivilegeEscalation": false, "capabilities": kube.Object{"drop": []string{"ALL"}}}
	spec := kube.Object{
		"restartPolicy": "Never", "activeDeadlineSeconds": seconds, "terminationGracePeriodSeconds": 1, "serviceAccountName": account, "automountServiceAccountToken": false,
		"securityContext": kube.Object{"runAsNonRoot": true, "runAsUser": 1000, "runAsGroup": 1000, "seccompProfile": kube.Object{"type": "RuntimeDefault"}},
	}
	if profile == "sandbox" {
		container = "agent"
		spec["runtimeClassName"] = sandboxNetworkRuntimeClass
		labels["agents.x-k8s.io/sandbox-name-hash"] = "network-fixture"
	} else if profile != "gateway" {
		panic("unknown network probe profile")
	}
	if gatewayLabel {
		labels["app.kubernetes.io/name"] = "openshell-gateway"
	}
	spec["containers"] = []any{kube.Object{"name": container,
		"image":           "docker.io/library/node@sha256:87362b5d965240a1bc79f85cec63179d4ee853741413b274a4721f2742eb8393",
		"imagePullPolicy": "IfNotPresent", "command": command, "securityContext": security,
		"resources": kube.Object{"requests": kube.Object{"cpu": "20m", "memory": "32Mi", "ephemeral-storage": "1Mi"}, "limits": kube.Object{"cpu": "100m", "memory": "128Mi", "ephemeral-storage": "16Mi"}},
	}}
	return kube.Object{"apiVersion": "v1", "kind": "Pod", "metadata": kube.Object{"name": name, "namespace": namespace, "labels": labels}, "spec": spec}
}

const sandboxNetworkListener = `
const net=require('node:net');
async function listen(port) {
 await new Promise((resolve,reject)=>net.createServer(s=>s.destroy()).on('error',reject).listen(port,'0.0.0.0',resolve));
 await new Promise((resolve,reject)=>{
  const s=net.createConnection({host:'127.0.0.1',port});
  s.setTimeout(2000,()=>{s.destroy();reject(new Error('timeout'));});
  s.on('connect',()=>{s.destroy();resolve();}); s.on('error',reject);
 });
}
(async()=>{await listen(2222);await listen(2223);console.log(JSON.stringify({ready:true,ports:[2222,2223]}));})()
 .catch(()=>{console.log(JSON.stringify({ready:false}));process.exit(1);});
setTimeout(()=>process.exit(0),300000);
`

type sandboxNetworkListenerRecord struct{ Namespace, Name, UID, Address string }

func (w *browserGatewayWorkload) startSandboxNetworkListener(id string) (sandboxNetworkListenerRecord, func()) {
	w.t.Helper()
	a := w.sandboxAllocations[id]
	if a.Account == "" || a.NamespaceUID == "" {
		w.t.Fatal("Sandbox allocation baseline is missing")
	}
	name := "network-listener-" + uuid.NewString()[:8]
	path := "/api/v1/namespaces/" + a.Namespace + "/pods"
	pod := boundedNetworkPod(a.Namespace, name, id, a.Account, "sandbox", false, []string{"node", "-e", sandboxNetworkListener}, 300)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	created, code, err := w.kubernetes.Request(ctx, http.MethodPost, path, pod)
	uid := kube.String(created, "metadata", "uid")
	if err != nil || code != 201 || uid == "" {
		w.t.Fatal("Sandbox listener creation failed", code, err)
	}
	path += "/" + name
	var once sync.Once
	cleanup := func() {
		once.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			_, code, err := w.kubernetes.Request(ctx, http.MethodDelete, path, kube.Object{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": kube.Object{"uid": uid}})
			if err != nil || (code != 200 && code != 202 && code != 404) {
				w.t.Error("Sandbox listener delete failed", code, err)
				return
			}
			for ctx.Err() == nil {
				_, code, err := w.kubernetes.Request(ctx, http.MethodGet, path, nil)
				if err == nil && code == 404 {
					return
				}
				time.Sleep(200 * time.Millisecond)
			}
			w.t.Error("Sandbox listener cleanup was not observed")
		})
	}
	w.t.Cleanup(cleanup)
	for ctx.Err() == nil {
		observed, code, err := w.kubernetes.Request(ctx, http.MethodGet, path, nil)
		if err != nil || code != 200 || kube.String(observed, "metadata", "uid") != uid {
			w.t.Fatal("Sandbox listener identity differs", code, err)
		}
		phase := kube.String(observed, "status", "phase")
		if phase == "Failed" || phase == "Succeeded" {
			w.t.Fatal("Sandbox listener stopped before its probes")
		}
		address := kube.String(observed, "status", "podIP")
		if phase == "Running" && net.ParseIP(address) != nil {
			result, code, err := w.kubernetes.Request(ctx, http.MethodGet, path+"/log?container=agent&tailLines=1&limitBytes=1024", nil)
			if err == nil && code == 200 && result["ready"] == true {
				ports, _ := json.Marshal(result["ports"])
				if string(ports) != "[2222,2223]" {
					w.t.Fatal("Sandbox listener did not check both ports")
				}
				return sandboxNetworkListenerRecord{a.Namespace, name, uid, address}, cleanup
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	w.t.Fatal("Sandbox listener did not become ready")
	return sandboxNetworkListenerRecord{}, cleanup
}

func (w *browserGatewayWorkload) checkSandboxNetworkIsolation(stage string) {
	w.t.Helper()
	if !w.nativeSandboxNetworkEnabled() {
		return
	}
	if len(w.gatewayIDs) != 2 || (stage != "initial" && stage != "after-recovery") {
		w.t.Fatal("native Sandbox network check needs two Gateways and a known stage")
	}
	var listeners []sandboxNetworkListenerRecord
	for _, id := range w.gatewayIDs {
		listener, cleanup := w.startSandboxNetworkListener(id)
		defer cleanup()
		listeners = append(listeners, listener)
	}
	var records []any
	for index, id := range w.gatewayIDs {
		gateway, err := gatewayworkload.Namespace(id)
		if err != nil {
			w.t.Fatal(err)
		}
		other, err := gatewayworkload.Namespace(w.gatewayIDs[1-index])
		if err != nil {
			w.t.Fatal(err)
		}
		targets := []gatewayNetworkTarget{
			{"assigned-gateway", "openshell-gateway." + gateway + ".svc.cluster.local", 8080, true},
			{"other-gateway", "openshell-gateway." + other + ".svc.cluster.local", 8080, false},
			{"control-api", "hypershell." + w.p.namespace + ".svc.cluster.local", 9090, false},
			{"postgres", w.databaseOptions.Host, int(w.databaseOptions.Port), false},
			{"kubernetes", "kubernetes.default.svc", 443, false},
		}
		// A timeout counts only after the target is known to accept connections.
		for _, target := range targets {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			conn, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(target.Host, strconv.Itoa(target.Port)))
			cancel()
			if err != nil {
				w.t.Fatal("Sandbox target has no live baseline", target.Name, err)
			}
			conn.Close()
		}
		targets = append(targets, gatewayNetworkTarget{"other-sandbox", listeners[1-index].Address, 2222, false})
		ingress := []gatewayNetworkTarget{
			{"assigned-sandbox", listeners[index].Address, 2222, true},
			{"other-sandbox", listeners[1-index].Address, 2222, false},
			{"unapproved-port", listeners[index].Address, 2223, false},
		}
		// The permitted parent source proves the listener before denied probes.
		parent := w.allocationNetworkProbe(gateway, id, "gateway", true, ingress)
		egress := w.allocationNetworkProbe(listeners[index].Namespace, id, "sandbox", false, targets)
		wrongLabel := w.allocationNetworkProbe(gateway, id, "gateway", false, []gatewayNetworkTarget{{"parent-without-gateway-label", listeners[index].Address, 2222, false}})
		records = append(records, map[string]any{"gateway_id": id, "listener": listeners[index], "parent_ingress": parent, "sandbox_egress": egress, "wrong_parent_label": wrongLabel})
	}
	if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
		data, err := json.MarshalIndent(map[string]any{"stage": stage, "runtime_class": sandboxNetworkRuntimeClass, "vm_isolation_tested": false, "openshell_execution_tested": false, "token_mounted": false, "fresh_connections": true, "gateways": records}, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(directory, "sandbox-network-"+stage+".json"), append(data, '\n'), 0600) != nil {
			w.t.Fatal("cannot save Sandbox network evidence")
		}
	}
}

// These Pods must not enter the real Gateway Service or get a credential mount.
func TestBoundedNetworkProbe(t *testing.T) {
	for _, profile := range []string{"gateway", "sandbox"} {
		for _, label := range []bool{false, true} {
			pod := boundedNetworkPod("allocated", "probe", "owner", "allocated-account", profile, label, []string{"node", "-e", "fixed probe"}, 90)
			spec := pod["spec"].(kube.Object)
			labels := pod["metadata"].(kube.Object)["labels"].(kube.Object)
			if labels["hypershell.redhat.io/gateway-id"] != nil {
				t.Fatal("probe entered the Gateway Service selector")
			}
			if spec["automountServiceAccountToken"] != false || spec["volumes"] != nil || spec["hostNetwork"] != nil || spec["hostPID"] != nil || spec["hostIPC"] != nil {
				t.Fatal("probe gained host or credential access")
			}
			if spec["activeDeadlineSeconds"] != 90 || spec["restartPolicy"] != "Never" {
				t.Fatal("probe lost its time bound")
			}
			if (spec["runtimeClassName"] == sandboxNetworkRuntimeClass) != (profile == "sandbox") || spec["runtimeClassName"] == "kata" {
				t.Fatal("probe used an incorrect runtime")
			}
			containers := spec["containers"].([]any)
			if len(containers) != 1 {
				t.Fatal("probe has an extra container")
			}
			container := containers[0].(kube.Object)
			if container["volumeMounts"] != nil || container["env"] != nil || container["envFrom"] != nil {
				t.Fatal("probe gained an external input")
			}
			security := container["securityContext"].(kube.Object)
			if security["privileged"] != nil || security["runAsNonRoot"] != true || security["runAsUser"] == 0 || security["allowPrivilegeEscalation"] != false {
				t.Fatal("probe security differs")
			}
			caps := security["capabilities"].(kube.Object)
			drops := caps["drop"].([]string)
			if caps["add"] != nil || len(drops) != 1 || drops[0] != "ALL" {
				t.Fatal("probe has extra capabilities")
			}
			limits := container["resources"].(kube.Object)["limits"].(kube.Object)
			if limits["cpu"] != "100m" || limits["memory"] != "128Mi" || limits["ephemeral-storage"] != "16Mi" {
				t.Fatal("probe resource bounds differ")
			}
		}
	}
}
