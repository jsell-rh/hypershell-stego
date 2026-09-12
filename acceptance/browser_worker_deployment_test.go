package acceptance

import (
	"encoding/json"
	"os"
	"strings"
)

func browserWorkerTelemetry(settings []string) []string {
	var result []string
	for _, entry := range settings {
		if strings.HasPrefix(entry, "OTEL_") {
			result = append(result, entry)
		}
	}
	return result
}

func (w *browserGatewayWorkload) startWorkers(address, ca string) {
	w.t.Helper()
	var endpoints []string
	if json.Unmarshal([]byte(os.Getenv("STEGO_TEST_KUBERNETES_EGRESS")), &endpoints) != nil || len(endpoints) == 0 || len(endpoints) > 16 {
		w.t.Fatal("Kubernetes endpoint bindings missing")
	}
	for _, worker := range []struct{ name, token, image string }{{"namespace-allocation", "allocation", "STEGO_TEST_ALLOCATION_WORKER_IMAGE"}, {"database", "database", "STEGO_TEST_DATABASE_WORKER_IMAGE"}, {"gateway-identity", "identity", "STEGO_TEST_IDENTITY_WORKER_IMAGE"}, {"gateway-workload", "workload", "STEGO_TEST_GATEWAY_WORKER_IMAGE"}} {
		name := "hypershell-" + worker.name
		image := os.Getenv(worker.image)
		if !strings.Contains(image, "@sha256:") {
			w.t.Fatal("worker image digest missing")
		}
		env := map[string]string{"HYPERSHELL_API_GRPC_ADDR": address, "HYPERSHELL_API_CA_FILE": "/var/run/stego/api-ca.pem", "HYPERSHELL_API_TOKEN_FILE": "/var/run/stego/api-token"}
		files := map[string][]byte{"api-ca.pem": w.p.read(ca), "api-token": []byte(w.tokens[worker.token])}
		w.p.settings(w.telemetry, env, files)
		target := []string{"--worker", worker.name}
		if worker.name == "gateway-identity" {
			env["HYPERSHELL_KEYCLOAK_URL"] = w.identity.options.ServerURL
			env["HYPERSHELL_KEYCLOAK_REALM"] = w.identity.options.Realm
			env["HYPERSHELL_KEYCLOAK_CLIENT_ID"] = w.identity.options.ClientID
			env["HYPERSHELL_KEYCLOAK_SECRET_FILE"] = "/var/run/stego/keycloak-secret"
			env["HYPERSHELL_KEYCLOAK_CA_FILE"] = "/var/run/stego/keycloak-ca.pem"
			files["keycloak-secret"] = w.p.read(w.identity.options.SecretFile)
			files["keycloak-ca.pem"] = w.p.read(w.identity.options.CAFile)
		} else {
			env["HYPERSHELL_CONTROL_NAMESPACE"] = w.p.namespace
			env["HYPERSHELL_MANAGED_CLUSTER_ID"] = w.f.cluster
			env["HYPERSHELL_KUBERNETES_URL"] = "https://kubernetes.default.svc"
			env["HYPERSHELL_KUBERNETES_CA_FILE"] = "/var/run/stego-kubernetes/ca.crt"
			env["HYPERSHELL_KUBERNETES_TOKEN_FILE"] = "/var/run/stego-kubernetes/token"
			for _, endpoint := range endpoints {
				target = append(target, "--egress", "kubernetes="+endpoint)
			}
			w.t.Cleanup(func() {
				w.p.command(nil, "delete", "clusterrole/"+w.p.namespace+"."+name, "clusterrolebinding/"+w.p.namespace+"."+name, "--ignore-not-found")
			})
			if worker.name == "database" {
				env["DATABASE_PROVIDER"] = "deployment"
				env["HYPERSHELL_MANAGED_CLUSTER_ID"] = w.f.cluster
				env["HYPERSHELL_DATABASE_CLUSTER_ISSUER"] = w.options.ClusterIssuer
			} else if worker.name == "gateway-workload" {
				env["HYPERSHELL_MANAGED_CLUSTER_ID"] = w.f.cluster
				env["HYPERSHELL_GATEWAY_CLUSTER_ISSUER"] = w.options.ClusterIssuer
				env["HYPERSHELL_GATEWAY_OIDC_ISSUER"] = w.identity.options.ServerURL + "/realms/workflow"
				env["HYPERSHELL_GATEWAY_TRUST_BUNDLE"] = "/var/run/stego/issuer-ca.pem"
				files["issuer-ca.pem"] = w.p.read(w.identity.options.CAFile)
				env["HYPERSHELL_GATEWAY_SANDBOX_IMAGE"] = sandboxImage
				env["HYPERSHELL_GATEWAY_SUPERVISOR_IMAGE"] = supervisorImage
			}
		}
		start := func() (func(), func() string) {
			return w.p.start(name, "..", image, testIdentity{}, env, files, target...)
		}
		stop, logs := start()
		previous := ""
		w.stops = append(w.stops, func() { stop() })
		w.outputs = append(w.outputs, func() string { return previous + logs() })
		w.restarts = append(w.restarts, func() { stop(); previous += logs(); stop, logs = start() })
	}
}
