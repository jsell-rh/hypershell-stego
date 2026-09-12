package acceptance

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	web "github.com/jsell-rh/hypershell-stego/out/application/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"github.com/jsell-rh/hypershell-stego/out/sdk"
	"google.golang.org/grpc/metadata"
)

// The generated API, generated identity worker, and real Keycloak server run
// in separate Pods. The Keycloak fixture is not a production deployment.
func checkKubernetesGatewayIdentity(t *testing.T, namespace string, apply func(any), command func([]byte, ...string) []byte, owner *sdk.Client, apiHost string, apiIdentity testIdentity, bearer, id string, exports []string) func() {
	t.Helper()
	k := startKubernetesKeycloak(t, namespace, apply, command)
	image := os.Getenv("STEGO_TEST_WORKER_IMAGE")
	group := os.Getenv("STEGO_TEST_FS_GROUP")
	if image == "" || group == "" {
		t.Fatal("require the generated worker image and file group")
	}
	const name = "hypershell-gateway-identity"
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	files := map[string][]byte{"api-ca.pem": read(apiIdentity.config.CAFile), "api-token": []byte(bearer), "keycloak-ca.pem": read(k.options.CAFile), "keycloak-secret": read(k.options.SecretFile)}
	environment := map[string]string{"HYPERSHELL_API_GRPC_ADDR": apiHost + ":9090", "HYPERSHELL_API_CA_FILE": "/var/run/stego/api-ca.pem", "HYPERSHELL_API_TOKEN_FILE": "/var/run/stego/api-token", "HYPERSHELL_KEYCLOAK_URL": k.options.ServerURL, "HYPERSHELL_KEYCLOAK_REALM": k.options.Realm, "HYPERSHELL_KEYCLOAK_CLIENT_ID": k.options.ClientID, "HYPERSHELL_KEYCLOAK_SECRET_FILE": "/var/run/stego/keycloak-secret", "HYPERSHELL_KEYCLOAK_CA_FILE": "/var/run/stego/keycloak-ca.pem"}
	for _, entry := range exports {
		key, value, _ := strings.Cut(entry, "=")
		switch key {
		case "OTEL_EXPORTER_OTLP_ENDPOINT":
			value = "https://fixture." + namespace + ".svc:19093"
		case "OTEL_EXPORTER_OTLP_CERTIFICATE":
			files["telemetry-ca.pem"] = read(value)
			value = "/var/run/stego/telemetry-ca.pem"
		case "OTEL_METRIC_EXPORT_INTERVAL":
			value = "10000"
		}
		environment[key] = value
	}
	t.Cleanup(func() {
		command(nil, "delete", "deployment/"+name, "--wait=true", "--timeout=60s", "--ignore-not-found")
		command(nil, "delete", "secret/"+name+"-files", "secret/"+name+"-runtime", "networkpolicy/"+name, "serviceaccount/"+name, "--ignore-not-found")
	})
	apply(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": name + "-files", "namespace": namespace}, "data": files})
	apply(map[string]any{"apiVersion": "v1", "kind": "Secret", "metadata": map[string]any{"name": name + "-runtime", "namespace": namespace}, "stringData": environment})
	renderContext, cancelRender := context.WithTimeout(context.Background(), 30*time.Second)
	render := exec.CommandContext(renderContext, "go", "run", "-mod=readonly", "../out/deploy/render", "--worker", "gateway-identity", "--image", image, "--namespace", namespace, "--fs-group", group)
	manifest, err := render.Output()
	cancelRender()
	if err != nil {
		t.Fatal("generated worker renderer failed", err)
	}
	command(manifest, "apply", "-f", "-")
	start := func() string {
		t.Helper()
		command(nil, "scale", "deployment/"+name, "--replicas=1")
		command(nil, "rollout", "status", "deployment/"+name, "--timeout=180s")
		var list struct {
			Items []struct {
				Metadata struct {
					UID               string
					DeletionTimestamp *string
				}
				Status struct{ ContainerStatuses []struct{ RestartCount int } }
			}
		}
		if err := json.Unmarshal(command(nil, "get", "pods", "-l", "app.kubernetes.io/name="+name, "-o", "json"), &list); err != nil {
			t.Fatal(err)
		}
		for _, pod := range list.Items {
			if pod.Metadata.DeletionTimestamp == nil {
				if len(pod.Status.ContainerStatuses) != 1 || pod.Status.ContainerStatuses[0].RestartCount != 0 {
					t.Fatal("generated worker restarted before readiness")
				}
				return pod.Metadata.UID
			}
		}
		t.Fatal("generated worker has no ready Pod")
		return ""
	}
	stop := func() {
		t.Helper()
		command(nil, "scale", "deployment/"+name, "--replicas=0")
		command(nil, "wait", "--for=delete", "pods", "-l", "app.kubernetes.io/name="+name, "--timeout=60s")
	}
	firstPod := start()
	_, connection := grpcClient(t, apiHost+":9090", apiIdentity)
	t.Cleanup(func() { connection.Close() })
	states := control.NewGatewayIdentityServiceClient(connection)
	expected, err := keycloak.GatewayClientID(id)
	if err != nil {
		t.Fatal(err)
	}
	wait := func() string {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		for {
			response, err := owner.GetGatewayWithResponse(ctx, id)
			var configured map[string]any
			if err == nil && response.JSON200 != nil && response.JSON200.Oidc != nil && json.Unmarshal([]byte(*response.JSON200.Oidc), &configured) == nil && configured["client_id"] == expected {
				state, err := states.GetGatewayIdentityState(metadata.NewOutgoingContext(ctx, metadata.Pairs("authorization", "Bearer "+bearer)), &control.GetGatewayIdentityStateRequest{Id: id})
				if err == nil {
					condition := state.GetConditions()["identity"].GetConditions()["ClientReady"]
					if condition.GetCurrent() && condition.GetStatus() == "True" && condition.GetObservedGeneration() == state.ResourceGeneration {
						return *response.JSON200.Oidc
					}
				}
			}
			select {
			case <-ctx.Done():
				t.Fatal("deployed Gateway identity did not converge")
			case <-time.After(100 * time.Millisecond):
			}
		}
	}
	first := wait()
	if live := k.gatewayClient(t, id); live == nil || live["publicClient"] != true || live["standardFlowEnabled"] != true || live["directAccessGrantsEnabled"] != false {
		t.Fatal("deployed Gateway has no safe browser client")
	}
	stop()
	// The API changes while the controller is absent. A new process must discover
	// this state through the generated scan, without a retained watch event.
	invalid := "invalid"
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	changed, err := owner.UpdateGatewayWithResponse(ctx, id, sdk.UpdateGatewayJSONRequestBody{Oidc: &invalid})
	if err != nil || changed.JSON200 == nil || changed.JSON200.Oidc == nil || *changed.JSON200.Oidc != invalid {
		t.Fatal("offline identity change failed", err)
	}
	if next := start(); next == firstPod || next == "" {
		t.Fatal("worker restart did not replace its Pod")
	}
	if wait() != first {
		t.Fatal("controller restart changed the Gateway identity")
	}
	// Leave the controller active during API Pod replacement. The returned check
	// proves a new state change converges after the API returns.
	return func() {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		response, err := owner.UpdateGatewayWithResponse(ctx, id, sdk.UpdateGatewayJSONRequestBody{Oidc: &invalid})
		if err != nil || response.JSON200 == nil {
			t.Fatal("identity change after API replacement failed", err)
		}
		if wait() != first {
			t.Fatal("API replacement changed the Gateway identity")
		}
		logs := command(nil, "logs", "deployment/"+name, "--tail=500")
		for _, private := range [][]byte{[]byte(bearer), files["keycloak-secret"]} {
			if bytes.Contains(logs, private) {
				t.Fatal("worker logs exposed credentials")
			}
		}
		stop()
		checkIdentityWorkerRunAborts(t, k, apiHost+":9090", apiIdentity.config.CAFile, bearer, buildProgram(t, "./out/deploy/workers/gateway-identity"), func() {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			response, err := owner.UpdateGatewayWithResponse(ctx, id, sdk.UpdateGatewayJSONRequestBody{Oidc: &invalid})
			if err != nil || response.JSON200 == nil {
				t.Fatal("worker abort reset failed", err)
			}
		}, wait, first)
		t.Log("Generated Gateway identity Deployment passed Keycloak creation, worker Pod replacement, and API Pod replacement")
	}
}

func startKubernetesKeycloak(t *testing.T, namespace string, apply func(any), command func([]byte, ...string) []byte) *keycloakFixture {
	t.Helper()
	type object = map[string]any
	name := "identity-fixture"
	host := name + "." + namespace + ".svc"
	identity := identity(t, host)
	dir := filepath.Dir(identity.config.CAFile)
	read := func(name string) []byte {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		return data
	}
	realmConfig := keycloakTestRealm()
	if os.Getenv("STEGO_TEST_BROWSER_WORKLOAD") == "1" {
		realmConfig["accessTokenLifespan"] = 900
	}
	realm, err := json.Marshal(realmConfig)
	if err != nil {
		t.Fatal(err)
	}
	meta := object{"name": name, "namespace": namespace}
	labels := object{"app": name}
	t.Cleanup(func() {
		command(nil, "delete", "pod/"+name, "--wait=true", "--timeout=60s", "--ignore-not-found")
		command(nil, "delete", "service/"+name, "secret/"+name, "networkpolicy/"+name, "--ignore-not-found")
	})
	apply(object{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": meta, "spec": object{
		"podSelector": object{"matchLabels": labels}, "policyTypes": []string{"Ingress", "Egress"}, "egress": []any{},
		"ingress": []any{object{"from": []any{object{"namespaceSelector": object{"matchLabels": object{"stego.test/browser-run": namespace}}, "podSelector": object{"matchExpressions": []any{object{"key": "hypershell.redhat.io/gateway-id", "operator": "Exists"}}}}, object{"podSelector": object{"matchLabels": object{"app": "stego-fixture"}}}, object{"podSelector": object{"matchLabels": object{"app.kubernetes.io/name": "hypershell-gateway-identity"}}}, object{"podSelector": object{"matchLabels": object{"app.kubernetes.io/name": "hypershell"}}}, object{"podSelector": object{"matchLabels": object{"app.kubernetes.io/name": "hypershell-console"}}}, object{"podSelector": object{"matchLabels": object{"app.kubernetes.io/name": "hypershell-provisioner"}}}}, "ports": []any{object{"protocol": "TCP", "port": 8443}}}},
	}})
	apply(object{"apiVersion": "v1", "kind": "Secret", "metadata": meta, "data": map[string][]byte{"tls.crt": read("server.pem"), "tls.key": read("server-key.pem"), "workflow-realm.json": realm}})
	apply(object{"apiVersion": "v1", "kind": "Service", "metadata": meta, "spec": object{"selector": labels, "ports": []any{object{"port": 8443}}}})
	apply(object{"apiVersion": "v1", "kind": "Pod", "metadata": object{"name": name, "namespace": namespace, "labels": labels}, "spec": object{
		"restartPolicy":                "Never",
		"automountServiceAccountToken": false, "terminationGracePeriodSeconds": 20, "activeDeadlineSeconds": 600,
		"securityContext": object{"runAsNonRoot": true, "seccompProfile": object{"type": "RuntimeDefault"}},
		"containers": []any{object{"name": "keycloak", "image": keycloakImage,
			"args":            []string{"start", "--db=dev-file", "--cache=local", "--http-enabled=false", "--hostname=https://" + host + ":8443", "--https-certificate-file=/certs/tls.crt", "--https-certificate-key-file=/certs/tls.key", "--https-protocols=TLSv1.3", "--import-realm"},
			"securityContext": object{"allowPrivilegeEscalation": false, "capabilities": object{"drop": []string{"ALL"}}},
			"resources":       object{"requests": object{"cpu": "100m", "memory": "256Mi", "ephemeral-storage": "128Mi"}, "limits": object{"cpu": "1", "memory": "1Gi", "ephemeral-storage": "1Gi"}},
			"readinessProbe":  object{"httpGet": object{"path": "/realms/workflow/.well-known/openid-configuration", "port": 8443, "scheme": "HTTPS"}, "timeoutSeconds": 2, "periodSeconds": 2, "failureThreshold": 90},
			"volumeMounts":    []any{object{"name": "tls", "mountPath": "/certs", "readOnly": true}, object{"name": "realm", "mountPath": "/opt/keycloak/data/import", "readOnly": true}},
		}},
		"volumes": []any{object{"name": "tls", "secret": object{"secretName": name, "defaultMode": 288, "items": []any{object{"key": "tls.crt", "path": "tls.crt"}, object{"key": "tls.key", "path": "tls.key"}}}}, object{"name": "realm", "secret": object{"secretName": name, "defaultMode": 288, "items": []any{object{"key": "workflow-realm.json", "path": "workflow-realm.json"}}}}},
	}})
	command(nil, "wait", "--for=condition=Ready", "pod/"+name, "--timeout=180s")
	client, err := web.New(web.Options{BaseURL: "https://" + host + ":8443", CAFile: identity.config.CAFile})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(client.Close)
	// Pod readiness can precede the Service endpoint update. Require a
	// successful verified request through the Service before using it.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for attempts := 1; ; attempts++ {
		response, err := client.Do(ctx, http.MethodGet, "/realms/workflow/.well-known/openid-configuration", nil, nil)
		if err == nil && response.StatusCode == 200 {
			t.Logf("Keycloak Service passed verified TLS after %d requests", attempts)
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("Keycloak fixture failed verified TLS", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	// Check both ports inside the Pod. The open HTTPS port proves that the
	// shell can make a TCP connection. Policy cannot conceal the HTTP port.
	command(nil, "exec", "pod/"+name, "--", "/bin/bash", "-c", "exec 4<>/dev/tcp/127.0.0.1/8443 || exit 2; exec 4>&-; if (exec 3<>/dev/tcp/127.0.0.1/8080) 2>/dev/null; then exit 1; fi")

	secret := filepath.Join(t.TempDir(), "admin-secret")
	if err := os.WriteFile(secret, []byte("acceptance-only-admin-secret"), 0600); err != nil {
		t.Fatal(err)
	}
	return &keycloakFixture{options: keycloak.Options{ServerURL: "https://" + host + ":8443", Realm: "workflow", ClientID: "provisioner", SecretFile: secret, CAFile: identity.config.CAFile}, http: client, certificate: filepath.Join(dir, "server.pem")}
}
