package gatewayworkload

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	workload "github.com/jsell-rh/hypershell-stego/out/workload"
)

func workloadTestInputs(public bool) (config, database, keys, server, publicServer object) {
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	config = object{"data": object{"gateway.toml": "test configuration", "trust.pem": "test trust"}}
	database = object{"uri": encode("test database uri"), "ca.crt": encode("test database trust")}
	keys = object{"key-encryption-key": encode("test encryption key"), "signing.pem": encode("test signing key"), "public.pem": encode("test public key"), "kid": encode("test key id")}
	server = object{"data": object{"tls.crt": encode("test internal certificate"), "tls.key": encode("test internal key")}}
	if public {
		publicServer = object{"data": object{"tls.crt": encode("test public certificate"), "tls.key": encode("test public private key")}}
	}
	return
}

func TestGatewayWorkloadPreservesRuntimeContract(t *testing.T) {
	gw, release := records(t)
	config, db, keys, server, public := workloadTestInputs(true)
	result, err := resources(gw, "allocated-gateway", release, oidcConfig{Issuer: "https://issuer.example", Audience: "gateway-client"}, config, db, keys, server, public)
	if err != nil || len(result) != 2 {
		t.Fatal("workload construction failed", err)
	}
	var deployment object
	for _, entry := range result {
		if entry.object["kind"] == "Deployment" {
			deployment = entry.object
		}
		if entry.object["kind"] == "Service" {
			if kube.String(entry.object, "spec", "type") != "ClusterIP" {
				t.Fatal("Gateway Service type changed")
			}
			ports := kube.Nested(entry.object, "spec", "ports").([]workload.Object)
			if len(ports) != 1 || ports[0]["port"] != 8080 || ports[0]["targetPort"] != "grpc" {
				t.Fatal("Gateway Service port changed")
			}
		}
	}
	spec := kube.Nested(deployment, "spec", "template", "spec").(map[string]any)
	if spec["serviceAccountName"] != "allocated-gateway" || spec["automountServiceAccountToken"] != true || kube.String(deployment, "spec", "strategy", "type") != "Recreate" {
		t.Fatal("Gateway execution contract changed")
	}
	containers := spec["containers"].([]workload.Object)
	if len(containers) != 1 || containers[0]["image"] != release.Image {
		t.Fatal("Gateway image changed")
	}
	c := object(containers[0])
	for key, want := range map[string]string{"cpu": "100m", "memory": "256Mi", "ephemeral-storage": "32Mi"} {
		if kube.String(c, "resources", "requests", key) != want {
			t.Fatal("Gateway resource request changed", key)
		}
	}
	for key, want := range map[string]string{"cpu": "500m", "memory": "512Mi", "ephemeral-storage": "256Mi"} {
		if kube.String(c, "resources", "limits", key) != want {
			t.Fatal("Gateway resource limit changed", key)
		}
	}
	if kube.Nested(c, "securityContext", "allowPrivilegeEscalation") != false || kube.Nested(c, "securityContext", "readOnlyRootFilesystem") != true || kube.String(object(spec), "securityContext", "seccompProfile", "type") != "RuntimeDefault" {
		t.Fatal("Gateway security settings changed")
	}
	for name, want := range map[string]string{"startupProbe": "/healthz", "readinessProbe": "/readyz", "livenessProbe": "/healthz"} {
		if kube.String(c, name, "httpGet", "path") != want || kube.String(c, name, "httpGet", "port") != "health" {
			t.Fatal("Gateway probe changed", name)
		}
	}
	raw, err := json.Marshal(resultObjects(result))
	if err != nil {
		t.Fatal(err)
	}
	for _, private := range []string{"test database uri", "test encryption key", "test signing key", "test internal key", "test public private key"} {
		if bytes.Contains(raw, []byte(private)) || bytes.Contains(raw, []byte(base64.StdEncoding.EncodeToString([]byte(private)))) {
			t.Fatal("workload contains Secret contents")
		}
	}
	if kube.String(deployment, "spec", "template", "metadata", "annotations", workload.ConfigurationAnnotation) == "" {
		t.Fatal("configuration digest missing")
	}
}

func resultObjects(result []resource) []object {
	objects := make([]object, 0, len(result))
	for _, entry := range result {
		objects = append(objects, entry.object)
	}
	return objects
}

func TestGatewayWorkloadDigestTracksVerifiedDependencies(t *testing.T) {
	gw, release := records(t)
	build := func(config, db, keys, server, public object) string {
		t.Helper()
		result, err := resources(gw, "allocated-gateway", release, oidcConfig{}, config, db, keys, server, public)
		if err != nil {
			t.Fatal(err)
		}
		for _, entry := range result {
			if entry.object["kind"] == "Deployment" {
				return kube.String(entry.object, "spec", "template", "metadata", "annotations", workload.ConfigurationAnnotation)
			}
		}
		t.Fatal("Deployment missing")
		return ""
	}
	config, db, keys, server, public := workloadTestInputs(true)
	initial := build(config, db, keys, server, public)
	for _, name := range []string{"configuration", "database", "keys", "internal TLS", "public TLS", "metadata"} {
		t.Run(name, func(t *testing.T) {
			config, db, keys, server, public := workloadTestInputs(true)
			changed := base64.StdEncoding.EncodeToString([]byte("changed test value"))
			switch name {
			case "configuration":
				config["data"].(object)["gateway.toml"] = "changed configuration"
			case "database":
				db["uri"] = changed
			case "keys":
				keys["signing.pem"] = changed
			case "internal TLS":
				server["data"].(object)["tls.key"] = changed
			case "public TLS":
				public["data"].(object)["tls.key"] = changed
			case "metadata":
				server["metadata"] = object{"resourceVersion": "new"}
				config["metadata"] = object{"resourceVersion": "new"}
			}
			same := initial == build(config, db, keys, server, public)
			if same != (name == "metadata") {
				t.Fatal("configuration digest does not match dependency change")
			}
		})
	}
}

func TestGatewayWorkloadRejectsInvalidDependenciesWithoutResources(t *testing.T) {
	for _, name := range []string{"invalid encoding", "oversized dependency", "missing data", "wrong data type", "missing environment key", "invalid image", "missing account"} {
		t.Run(name, func(t *testing.T) {
			gw, release := records(t)
			config, db, keys, server, public := workloadTestInputs(true)
			account := "allocated-gateway"
			switch name {
			case "invalid encoding":
				db["uri"] = "private invalid encoding"
			case "oversized dependency":
				db["uri"] = strings.Repeat("a", (2<<20)+1)
			case "missing data":
				server = object{}
			case "wrong data type":
				config["data"] = []string{"private invalid data"}
			case "missing environment key":
				delete(keys, "key-encryption-key")
			case "invalid image":
				release.Image = "registry.example/image:latest"
			case "missing account":
				account = "default"
			}
			result, err := resources(gw, account, release, oidcConfig{}, config, db, keys, server, public)
			if err == nil || result != nil {
				t.Fatal("invalid declaration returned resources")
			}
			if strings.Contains(err.Error(), "private") {
				t.Fatal("error contains dependency contents")
			}
		})
	}
}
