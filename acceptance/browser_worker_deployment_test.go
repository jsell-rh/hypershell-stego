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
	for _, worker := range []struct{ name, token, image string }{{"namespace-allocation", "allocation", "STEGO_TEST_ALLOCATION_WORKER_IMAGE"}, {"gateway-identity", "identity", "STEGO_TEST_IDENTITY_WORKER_IMAGE"}, {"gateway-workload", "workload", "STEGO_TEST_GATEWAY_WORKER_IMAGE"}} {
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
			if w.endpointChange != nil {
				target = append(target, "--egress", "network-probe="+w.endpointChange.Initial)
			}
			if worker.name == "gateway-workload" {
				env["HYPERSHELL_GATEWAY_DATABASE_CONFIG_FILE"] = "/var/run/stego/gateway-database.json"
				files["gateway-database.json"] = w.databaseConfig
				for _, endpoint := range w.databaseEndpoints {
					target = append(target, "--egress", "gateway-postgres="+endpoint)
				}
				target = w.publicWorkerSettings(env, files, target)
				env["HYPERSHELL_MANAGED_CLUSTER_ID"] = w.f.cluster
				env["HYPERSHELL_GATEWAY_CLUSTER_ISSUER"] = w.options.ClusterIssuer
				env["HYPERSHELL_GATEWAY_INTERNAL_CA_FILE"] = "/var/run/stego/gateway-internal-ca.pem"
				files["gateway-internal-ca.pem"] = w.internalCA
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
		if w.endpointChange != nil && worker.name != "gateway-identity" {
			w.endpointRestarts = append(w.endpointRestarts, func(address string) {
				stop()
				previous += logs()
				found := 0
				for i, value := range target {
					if strings.HasPrefix(value, "network-probe=") {
						target[i] = "network-probe=" + address
						found++
					}
				}
				if found != 1 {
					w.t.Fatal("worker endpoint binding is missing or repeated")
				}
				stop, logs = start()
			})
		}
		if worker.name == "gateway-workload" && w.public != nil {
			w.publicEgressFailure = func(id string) {
				original := append([]string{}, target...)
				w.checkPublicEgressLoss(id, func() {
					stop()
					previous += logs()
					target = withoutPublicEgress(original)
					stop, logs = start()
				}, func() {
					stop()
					previous += logs()
					target = original
					stop, logs = start()
				})
			}
		}
		w.stops = append(w.stops, func() { stop() })
		w.outputs = append(w.outputs, func() string { return previous + logs() })
		w.restarts = append(w.restarts, func() { stop(); previous += logs(); stop, logs = start() })
	}
}
