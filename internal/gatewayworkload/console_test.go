package gatewayworkload

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	postgres "github.com/jsell-rh/hypershell-stego/out/postgres"
)

func TestConsoleResourcesKeepGatewayServiceSeparate(t *testing.T) {
	gw, release := records(t)
	digest := strings.Repeat("a", 64)
	entries, err := consoleResources(gw, release.Image, 65532, digest)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		t.Fatal("unexpected resource count", len(entries))
	}
	kinds := map[string]bool{}
	for _, entry := range entries {
		kind := kube.String(entry.object, "kind")
		kinds[kind] = true
		if !consoleOwner(gw.Metadata.Id).Matches(entry.object) {
			t.Fatal("resource has no console owner", kind)
		}
		if kube.String(entry.object, "metadata", "namespace") != gw.Namespace {
			t.Fatal("resource escaped Gateway namespace", kind)
		}
		if kind == "Deployment" {
			if kube.String(entry.object, "spec", "template", "metadata", "labels", ownerLabel) != "" {
				t.Fatal("console Pod matches Gateway Service")
			}
			if kube.String(entry.object, "spec", "template", "metadata", "annotations", "hypershell.redhat.io/console-configuration") != digest {
				t.Fatal("configuration does not trigger rollout")
			}
		}
	}
	if !kinds["Service"] || !kinds["ServiceAccount"] || !kinds["Deployment"] || kinds["NetworkPolicy"] {
		t.Fatal("worker resource set is incorrect", kinds)
	}
	for _, invalid := range []string{"", strings.Repeat("A", 64), strings.Repeat("a", 63)} {
		if _, err := consoleResources(gw, release.Image, 65532, invalid); err == nil {
			t.Fatal("invalid digest accepted")
		}
	}
	gw.Namespace = "foreign"
	if _, err := consoleResources(gw, release.Image, 65532, digest); err == nil {
		t.Fatal("foreign namespace accepted")
	}
}

func consoleDependencies(id string) []object {
	ns, _ := Namespace(id)
	values := make([]object, len(consoleSecretNames))
	for i, name := range consoleSecretNames {
		values[i] = object{"apiVersion": "v1", "kind": "Secret", "metadata": object{"namespace": ns, "name": name, "uid": "uid", "resourceVersion": "1", "labels": object{ownerLabel: id, consoleComponentLabel: "gateway-console"}}, "type": "Opaque", "data": object{"value": "YWJj"}}
	}
	return values
}

func TestConsoleConfigurationTracksContents(t *testing.T) {
	gw, _ := records(t)
	secrets := consoleDependencies(gw.Metadata.Id)
	first, err := consoleConfigurationDigest(gw.Metadata.Id, secrets)
	if err != nil {
		t.Fatal(err)
	}
	secrets[0]["metadata"].(object)["resourceVersion"] = "2"
	next, err := consoleConfigurationDigest(gw.Metadata.Id, secrets)
	if err != nil || first != next {
		t.Fatal("metadata update changed configuration", err)
	}
	secrets[0]["data"].(object)["value"] = "ZGVm"
	next, err = consoleConfigurationDigest(gw.Metadata.Id, secrets)
	if err != nil || first == next {
		t.Fatal("credential change did not change configuration", err)
	}
}

func TestConsoleConfigurationRejectsInvalidDependencies(t *testing.T) {
	gw, _ := records(t)
	cases := map[string]func([]object) []object{
		"missing":      func(s []object) []object { return s[:3] },
		"owner":        func(s []object) []object { s[0]["metadata"].(object)["labels"] = object{}; return s },
		"deleted":      func(s []object) []object { s[0]["metadata"].(object)["deletionTimestamp"] = "now"; return s },
		"uid":          func(s []object) []object { delete(s[0]["metadata"].(object), "uid"); return s },
		"revision":     func(s []object) []object { delete(s[0]["metadata"].(object), "resourceVersion"); return s },
		"name":         func(s []object) []object { s[0]["metadata"].(object)["name"] = "foreign"; return s },
		"type":         func(s []object) []object { s[0]["type"] = "kubernetes.io/tls"; return s },
		"empty":        func(s []object) []object { s[0]["data"] = object{}; return s },
		"base64":       func(s []object) []object { s[0]["data"] = object{"value": "not base64"}; return s },
		"noncanonical": func(s []object) []object { s[0]["data"] = object{"value": "YWJj\n"}; return s },
		"entry type":   func(s []object) []object { s[0]["data"] = object{"value": 42}; return s },
		"key":          func(s []object) []object { s[0]["data"] = object{"../value": "YWJj"}; return s },
		"bound":        func(s []object) []object { s[0]["data"] = object{"value": strings.Repeat("a", (128<<10)+1)}; return s },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := consoleConfigurationDigest(gw.Metadata.Id, change(consoleDependencies(gw.Metadata.Id))); err == nil {
				t.Fatal("invalid dependency accepted")
			}
		})
	}
}

func TestConsoleWaitsForAssignedNamespace(t *testing.T) {
	for _, mode := range []string{"missing", "foreign", "wrong cluster", "wrong namespace"} {
		t.Run(mode, func(t *testing.T) {
			gw, _ := records(t)
			calls := 0
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != http.MethodGet || r.URL.Path != "/api/v1/namespaces/"+gw.Namespace {
					t.Error("console accessed a dependency before allocation")
					w.WriteHeader(500)
					return
				}
				if mode == "missing" {
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(object{"metadata": object{"uid": "foreign", "resourceVersion": "1", "labels": object{}}})
			})
			if mode == "wrong cluster" {
				gw.ClusterId = "foreign"
			}
			if mode == "wrong namespace" {
				gw.Namespace = "foreign"
			}
			err := k.EnsureConsole(context.Background(), gw, 1, 65532)
			if err == nil {
				t.Fatal("console accepted an invalid allocation")
			}
			if mode == "missing" && !errors.Is(err, ErrPending) {
				t.Fatal("missing allocation did not wait", err)
			}
			if (mode == "wrong cluster" || mode == "wrong namespace") && calls != 0 {
				t.Fatal("invalid placement reached Kubernetes")
			}
		})
	}
}

func TestConsoleStoreDependenciesMatchRetainedState(t *testing.T) {
	gw, _ := records(t)
	encode := func(value string) string { return base64.StdEncoding.EncodeToString([]byte(value)) }
	expected := object{"database-url": encode("postgresql://runtime:private@database.example/console?sslmode=verify-full"), "database-ca.pem": encode("fixture-ca"), "session-key": encode("fixture-key")}
	for _, mode := range []string{"valid", "database", "key", "direct-url", "key-path", "application"} {
		t.Run(mode, func(t *testing.T) {
			secrets := consoleDependencies(gw.Metadata.Id)
			secrets[0]["data"] = object{"DATABASE_URL_FILE": encode("/var/run/stego/database-url"), "STEGO_BROWSER_SESSION_KEY_FILE": encode("/var/run/stego/session-key")}
			files := object{}
			for name, value := range expected {
				files[name] = value
			}
			secrets[1]["data"] = files
			switch mode {
			case "database":
				files["database-url"] = encode("other-database")
			case "key":
				delete(files, "session-key")
			case "direct-url":
				secrets[0]["data"].(object)["DATABASE_URL"] = encode("other-database")
			case "key-path":
				secrets[0]["data"].(object)["STEGO_BROWSER_SESSION_KEY_FILE"] = encode("/var/run/stego/other-key")
			case "application":
				secrets[3]["data"].(object)["session-key"] = expected["session-key"]
			}
			err := consoleStoreDependencies(secrets, expected)
			if (err == nil) != (mode == "valid") {
				t.Fatal("console storage boundary differs", mode, err)
			}
		})
	}
}

func TestConsoleStateValidatesNewAndStoredData(t *testing.T) {
	k := &Kubernetes{options: Options{ClusterID: testClusterID}}
	config := postgres.Options{Host: "database.example", Port: 5432, Database: "ledger"}
	encode := func(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }
	fresh := object{"data": object{"database-password": encode(strings.Repeat("a", 64)), "database-server": encode(strings.Repeat("b", 64)), "database-destination": encode(k.destination(config)), "session-key": encode(strings.Repeat("k", 32))}}
	for _, stored := range []bool{false, true} {
		for _, mode := range []string{"valid", "extra", "missing", "short key", "wrong destination"} {
			t.Run(fmt.Sprintf("stored=%t/%s", stored, mode), func(t *testing.T) {
				raw, _ := json.Marshal(fresh)
				var value object
				if err := json.Unmarshal(raw, &value); err != nil {
					t.Fatal(err)
				}
				fields := value["data"].(map[string]any)
				switch mode {
				case "extra":
					fields["other"] = encode("other")
				case "missing":
					delete(fields, "database-server")
				case "short key":
					fields["session-key"] = encode("short")
				case "wrong destination":
					fields["database-destination"] = encode("other")
				}
				if !stored {
					value["data"] = object(fields)
				}
				err := k.validateConsoleState(value, config)
				if (err == nil) != (mode == "valid") {
					t.Fatal("console state validation differs", err)
				}
			})
		}
	}
}
