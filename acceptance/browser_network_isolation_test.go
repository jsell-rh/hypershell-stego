package acceptance

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/google/uuid"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkload"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

const gatewayNetworkProbe = `
const net = require('node:net');
const dns = require('node:dns').promises;
const targets = JSON.parse(process.argv[1]);
async function check(target) {
 const address = await dns.lookup(target.host);
 const outcome = await new Promise(resolve => {
  const socket = net.createConnection({host:address.address, port:target.port});
  let done = false;
  function end(value) { if (!done) { done=true; socket.destroy(); resolve(value); } }
  socket.setTimeout(2500, () => end('timeout'));
  socket.on('connect', () => end('connected'));
  socket.on('error', e => end(e.code));
 });
 return {name:target.name, allowed:target.allowed, outcome, passed:outcome === (target.allowed ? 'connected' : 'timeout')};
}
(async () => {
 const results=[];
 for (const target of targets) results.push(await check(target));
 console.log(JSON.stringify({results}));
 process.exitCode=results.every(r=>r.passed) ? 0 : 1;
})().catch(()=>{console.log(JSON.stringify({error:'probe setup failed'}));process.exitCode=1;});
`

type gatewayNetworkTarget struct {
	Name    string `json:"name"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	Allowed bool   `json:"allowed"`
}

// The probe uses the Gateway namespace policy, with no token or Secret mount.
// The complete workflow separately checks TLS and application access rules.
func (w *browserGatewayWorkload) checkGatewayNetworkIsolation(stage string) {
	w.t.Helper()
	if len(w.gatewayIDs) != 2 {
		w.t.Fatal("network isolation requires two Gateways")
	}
	issuer, err := url.Parse(w.identity.options.ServerURL)
	if err != nil {
		w.t.Fatal(err)
	}
	port, err := strconv.Atoi(issuer.Port())
	if err != nil {
		w.t.Fatal("identity fixture port is missing")
	}
	records := []any{}
	for index, id := range w.gatewayIDs {
		namespace, err := gatewayworkload.Namespace(id)
		if err != nil {
			w.t.Fatal(err)
		}
		other, err := gatewayworkload.Namespace(w.gatewayIDs[1-index])
		if err != nil {
			w.t.Fatal(err)
		}
		targets := []gatewayNetworkTarget{
			{"kubernetes", "kubernetes.default.svc", 443, true},
			{"postgres", w.databaseOptions.Host, int(w.databaseOptions.Port), true},
			{"identity", issuer.Hostname(), port, true},
			{"telemetry", w.p.host("fixture"), 19093, true},
			{"other-gateway", "openshell-gateway." + other + ".svc.cluster.local", 8080, false},
			{"control-api", "hypershell." + w.p.namespace + ".svc.cluster.local", 9090, false},
		}
		// Confirm denied targets are live from the permitted fixture before the
		// probe runs. Refused connections or DNS failures do not count as isolation.
		for _, target := range targets {
			if target.Allowed {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", net.JoinHostPort(target.Host, strconv.Itoa(target.Port)))
			cancel()
			if err != nil {
				w.t.Fatal("denied network target has no live baseline", target.Name, err)
			}
			connection.Close()
		}
		result := w.gatewayNetworkProbe(namespace, id, targets)
		records = append(records, map[string]any{"gateway_id": id, "namespace": namespace, "result": result})
	}
	if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
		data, err := json.MarshalIndent(map[string]any{"stage": stage, "fresh_connections": true, "token_mounted": false, "gateways": records}, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(directory, "gateway-network-"+stage+".json"), append(data, '\n'), 0600) != nil {
			w.t.Fatal("cannot save Gateway network evidence")
		}
	}
}

func (w *browserGatewayWorkload) gatewayNetworkProbe(namespace, id string, targets []gatewayNetworkTarget) kube.Object {
	w.t.Helper()
	encoded, err := json.Marshal(targets)
	if err != nil {
		w.t.Fatal(err)
	}
	name := "network-probe-" + uuid.NewString()[:8]
	collection := "/api/v1/namespaces/" + namespace + "/pods"
	pod := kube.Object{"apiVersion": "v1", "kind": "Pod", "metadata": kube.Object{"name": name, "namespace": namespace, "labels": kube.Object{"stego.test/network-probe": id}}, "spec": kube.Object{
		"restartPolicy": "Never", "activeDeadlineSeconds": 90, "terminationGracePeriodSeconds": 1, "serviceAccountName": "openshell-gateway", "automountServiceAccountToken": false,
		"securityContext": kube.Object{"runAsNonRoot": true, "runAsUser": 1000, "runAsGroup": 1000, "seccompProfile": kube.Object{"type": "RuntimeDefault"}},
		"containers": []any{kube.Object{"name": "probe", "image": "docker.io/library/node@sha256:87362b5d965240a1bc79f85cec63179d4ee853741413b274a4721f2742eb8393", "imagePullPolicy": "IfNotPresent", "command": []string{"node", "-e", gatewayNetworkProbe, string(encoded)},
			"securityContext": kube.Object{"readOnlyRootFilesystem": true, "allowPrivilegeEscalation": false, "capabilities": kube.Object{"drop": []string{"ALL"}}},
			"resources":       kube.Object{"requests": kube.Object{"cpu": "20m", "memory": "32Mi", "ephemeral-storage": "1Mi"}, "limits": kube.Object{"cpu": "100m", "memory": "128Mi", "ephemeral-storage": "16Mi"}},
		}},
	}}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	created, code, err := w.kubernetes.Request(ctx, http.MethodPost, collection, pod)
	uid := kube.String(created, "metadata", "uid")
	if err != nil || code != 201 || uid == "" {
		w.t.Fatal("network probe creation failed", code, err)
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 45*time.Second)
		defer stop()
		_, _, err := w.kubernetes.Request(cleanup, http.MethodDelete, collection+"/"+name, kube.Object{"apiVersion": "v1", "kind": "DeleteOptions", "preconditions": kube.Object{"uid": uid}})
		if err != nil {
			w.t.Error("network probe cleanup failed", err)
			return
		}
		for {
			_, status, err := w.kubernetes.Request(cleanup, http.MethodGet, collection+"/"+name, nil)
			if err == nil && status == 404 {
				return
			}
			if cleanup.Err() != nil {
				w.t.Error("network probe cleanup was not observed")
				return
			}
			time.Sleep(200 * time.Millisecond)
		}
	}()
	for {
		observed, status, err := w.kubernetes.Request(ctx, http.MethodGet, collection+"/"+name, nil)
		if err != nil || status != 200 || kube.String(observed, "metadata", "uid") != uid {
			w.t.Fatal("network probe observation failed", status, err)
		}
		phase := kube.String(observed, "status", "phase")
		if phase == "Succeeded" || phase == "Failed" {
			result, status, err := w.kubernetes.Request(ctx, http.MethodGet, collection+"/"+name+"/log?container=probe&tailLines=1&limitBytes=8192", nil)
			if err != nil || status != 200 {
				w.t.Fatal("network probe evidence unavailable", status, err)
			}
			if phase != "Succeeded" {
				w.t.Fatal("Gateway network isolation failed", fmt.Sprint(result))
			}
			rows, ok := result["results"].([]any)
			if !ok || len(rows) != len(targets) {
				w.t.Fatal("network probe evidence is incomplete")
			}
			for index, raw := range rows {
				row, ok := raw.(map[string]any)
				if !ok || row["name"] != targets[index].Name || row["allowed"] != targets[index].Allowed || row["passed"] != true {
					w.t.Fatal("network probe result differs")
				}
			}
			return result
		}
		if ctx.Err() != nil {
			w.t.Fatal("network probe did not finish")
		}
		time.Sleep(500 * time.Millisecond)
	}
}
