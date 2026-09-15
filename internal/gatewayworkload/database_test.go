package gatewayworkload

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func TestDurableGatewayStateSurvivesRestartAndRejectsLoss(t *testing.T) {
	for _, mode := range []string{"restart", "credential-rotation", "destination-change", "missing-secret", "missing-marker", "changed-secret", "foreign-owner", "unsealed"} {
		t.Run(mode, func(t *testing.T) {
			gw, _ := records(t)
			var namespace, secret, marker object
			writes := 0
			k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != http.MethodGet {
					writes++
					w.WriteHeader(500)
					return
				}
				var value object
				switch {
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
			switch mode {
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
