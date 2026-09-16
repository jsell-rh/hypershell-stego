package gatewayworkload

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func TestDurableGatewayStateSurvivesRestartAndRejectsLoss(t *testing.T) {
	for _, mode := range []string{"restart", "credential-rotation", "destination-change", "missing-secret", "missing-marker", "changed-secret", "foreign-owner", "unsealed", "missing-policy", "changed-policy", "extra-policy"} {
		t.Run(mode, func(t *testing.T) {
			gw, _ := records(t)
			var namespace, secret, marker, policy object
			writes := 0
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					w.WriteHeader(500)
					return
				}
				var value object
				switch {
				case strings.HasSuffix(r.URL.Path, "/networkpolicies/stego-allocation"):
					value = policy
				case strings.HasSuffix(r.URL.Path, "/networkpolicies"):
					items := []any{policy}
					if mode == "extra-policy" {
						items = append(items, object{"metadata": object{"name": "extra"}})
					}
					value = object{"metadata": object{"resourceVersion": "1"}, "items": items}
				case strings.HasSuffix(r.URL.Path, "/secrets/"+stateSecret):
					value = secret
				case strings.HasSuffix(r.URL.Path, "/configmaps/"+stateIdentity):
					value = marker
				case strings.Contains(r.URL.Path, "/namespaces/"):
					value = namespace
				default:
					w.WriteHeader(500)
					return
				}
				if value == nil {
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(value)
			})
			config := databaseConfig{Host: "postgres.example.test", Port: 5432, Database: "provisioning", User: "administrator", Password: "private-admin-password", CA: k.trust}
			save := func() {
				encoded, _ := json.Marshal(config)
				if err := os.WriteFile(k.options.DatabaseConfigFile, encoded, 0600); err != nil {
					t.Fatal(err)
				}
			}
			save()
			options, err := k.databaseConfig()
			if err != nil {
				t.Fatal(err)
			}
			values, err := newKeys()
			if err != nil {
				t.Fatal(err)
			}
			for key, value := range map[string]string{"database-password": strings.Repeat("a", 64), "database-server": strings.Repeat("b", 64), "database-destination": k.destination(options)} {
				values[key] = base64.StdEncoding.EncodeToString([]byte(value))
			}
			secret = k.stateDefinition("Secret", stateSecret, gw.Metadata.Id)
			secret["data"] = values
			meta := secret["metadata"].(object)
			meta["uid"] = "source"
			meta["resourceVersion"] = "1"
			fingerprint, err := stateFingerprint(secret)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := k.options.SQLBindings.Bind(context.Background(), gw.Metadata.Id, k.options.ClusterID, fingerprint); err != nil {
				t.Fatal(err)
			}
			marker = k.stateDefinition("ConfigMap", stateIdentity, gw.Metadata.Id)
			marker["data"] = object{"sha256": fingerprint}
			meta = marker["metadata"].(object)
			meta["uid"] = "marker"
			meta["resourceVersion"] = "2"
			ns, _ := StateNamespace(gw.Metadata.Id)
			namespace = k.stateDefinition("Namespace", ns, gw.Metadata.Id)
			meta = namespace["metadata"].(object)
			meta["uid"] = "namespace"
			meta["resourceVersion"] = "3"
			meta["annotations"] = object{stateAnnotation: fingerprint}
			policy = stateNetworkPolicyFixture(k, gw.Metadata.Id)
			switch mode {
			case "missing-policy":
				policy = nil
			case "changed-policy":
				policy["spec"].(object)["egress"] = []any{object{}}
			case "credential-rotation":
				config.User = "replacement-admin"
				config.Password = "replacement-password"
				save()
			case "destination-change":
				config.Host = "other.example.test"
				save()
			case "missing-secret":
				secret = nil
			case "missing-marker":
				marker = nil
			case "changed-secret":
				secret["data"].(object)["database-password"] = base64.StdEncoding.EncodeToString([]byte(strings.Repeat("c", 64)))
			case "foreign-owner":
				secret["metadata"].(object)["labels"].(object)[ownerLabel] = "other"
			case "unsealed":
				delete(meta["annotations"].(object), stateAnnotation)
			}
			recovered, selected, err := k.readLocalState(context.Background(), gw)
			if mode == "restart" || mode == "credential-rotation" {
				if err != nil || selected.ServerIdentity != strings.Repeat("b", 64) || kube.String(recovered, "data", "database-password") != values["database-password"] {
					t.Fatal("durable state changed", err)
				}
			} else if err == nil {
				t.Fatal("invalid state was accepted")
			}
			if mode == "unsealed" && !errors.Is(err, ErrPending) {
				t.Fatal("unsealed state did not wait", err)
			}
			if writes != 0 {
				t.Fatal("state recovery changed durable resources")
			}
		})
	}
}

func TestGatewayStateNamesPreserveEveryIDByte(t *testing.T) {
	gw, _ := records(t)
	name, err := StateNamespace(gw.Metadata.Id)
	if err != nil || len(name) != len("openshell-state-")+40 {
		t.Fatal("state name lost ID bytes", err)
	}
	if _, err = StateNamespace("invalid"); err == nil {
		t.Fatal("invalid ID accepted")
	}
}

// State storage has no Pod traffic. Supply its explicit deny policy in the
// HTTPS fixture so recovery still checks the generated allocation boundary.
func stateNetworkPolicyFixture(k *Kubernetes, id string) object {
	namespace, _ := StateNamespace(id)
	encoded, _ := json.Marshal(object{"podSelector": object{}})
	digest := sha256.Sum256(encoded)
	return object{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy",
		"metadata": object{"name": "stego-allocation", "namespace": namespace, "uid": namespace + "-policy", "resourceVersion": "1", "labels": k.stateOwner(id), "annotations": object{"stego.dev/network-spec-sha256": hex.EncodeToString(digest[:])}},
		"spec":     object{"podSelector": object{}, "policyTypes": []string{"Ingress", "Egress"}}}
}

func (k *Kubernetes) stateDefinition(kind, name, id string) object {
	value := definition("v1", kind, name, id)
	labels := value["metadata"].(object)["labels"].(object)
	for key, val := range k.stateOwner(id) {
		labels[key] = val
	}
	if kind != "Namespace" {
		ns, _ := StateNamespace(id)
		value["metadata"].(object)["namespace"] = ns
	}
	if kind == "Secret" {
		value["type"] = "Opaque"
	}
	value["immutable"] = true
	return value
}

// Retain the prior data hash in this fixture to check stored-state compatibility.
func stateFingerprint(secret object) (string, error) {
	values := secret["data"]
	switch values.(type) {
	case object, map[string]any:
	default:
		return "", errors.New("Gateway state has no data")
	}
	encoded, err := json.Marshal(values)
	if err != nil {
		return "", errors.New("Gateway state is invalid")
	}
	return hex.EncodeToString(sha256sum(encoded)), nil
}
