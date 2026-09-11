package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	web "github.com/jsell-rh/hypershell-stego/out/application/client"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"github.com/jsell-rh/hypershell-stego/out/sdk"
	"google.golang.org/grpc/metadata"
)

// The controller runs as a separate process in the bounded test Pod. The API
// and real Keycloak server run in separate Pods. This is an application check;
// the Keycloak fixture is not a production deployment.
func checkKubernetesGatewayIdentity(t *testing.T, namespace string, apply func(any), command func([]byte, ...string) []byte, owner *sdk.Client, apiHost string, apiIdentity testIdentity, bearer, id string) func() {
	t.Helper()
	k := startKubernetesKeycloak(t, namespace, apply, command)
	binary := buildProgram(t, "./cmd/gateway-identity-controller")
	stop, _ := startIdentityController(t, binary, k, apiHost+":9090", apiIdentity.config.CAFile, bearer)
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
	stop, _ = startIdentityController(t, binary, k, apiHost+":9090", apiIdentity.config.CAFile, bearer)
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
		stop()
		t.Log("Gateway identity passed real Keycloak creation, controller restart, and API Pod replacement")
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
	realm, err := json.Marshal(keycloakTestRealm())
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
		"ingress": []any{object{"from": []any{object{"podSelector": object{"matchLabels": object{"app": "stego-fixture"}}}}, "ports": []any{object{"protocol": "TCP", "port": 8443}}}},
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
	return &keycloakFixture{options: keycloak.Options{ServerURL: "https://" + host + ":8443", Realm: "workflow", ClientID: "provisioner", SecretFile: secret, CAFile: identity.config.CAFile}, http: client}
}
