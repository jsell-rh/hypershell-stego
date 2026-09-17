package gatewayworkload

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
)

func TestWorkloadsWaitForAllocatedAccountsBeforeDependencies(t *testing.T) {
	for _, workload := range []string{"gateway", "console"} {
		for _, mode := range []string{"missing", "foreign", "deleting", "automatic token", "denied", "missing annotation"} {
			t.Run(workload+"/"+mode, func(t *testing.T) {
				gw, release := records(t)
				var namespace, account object
				accountPath := ""
				reads := 0
				k := fixture(t, func(w http.ResponseWriter, r *http.Request) {
					reads++
					if r.Method != http.MethodGet {
						t.Error("workload wrote before its account was ready")
						w.WriteHeader(http.StatusInternalServerError)
						return
					}
					switch r.URL.Path {
					case "/api/v1/namespaces/" + gw.Namespace:
						_ = json.NewEncoder(w).Encode(namespace)
					case accountPath:
						if mode == "missing" {
							w.WriteHeader(http.StatusNotFound)
						} else if mode == "denied" {
							w.WriteHeader(http.StatusForbidden)
						} else {
							_ = json.NewEncoder(w).Encode(account)
						}
					default:
						t.Error("workload accessed a dependency before its account was ready")
						w.WriteHeader(http.StatusInternalServerError)
					}
				})
				labels := object{allocation.MarkerLabel: k.allocation.Marker(), allocation.ProfileLabel: "gateway", ownerLabel: gw.Metadata.Id, managerLabel: manager}
				annotations := object{}
				for _, alias := range []string{"gateway", "console"} {
					name, err := k.allocation.ServiceAccountName("gateway", gw.Namespace, gw.Metadata.Id, alias)
					if err != nil {
						t.Fatal(err)
					}
					annotations["stego.dev/service-account-"+alias] = name
				}
				name := annotations["stego.dev/service-account-"+workload].(string)
				accountPath = "/api/v1/namespaces/" + gw.Namespace + "/serviceaccounts/" + name
				namespace = object{"apiVersion": "v1", "kind": "Namespace", "metadata": object{"name": gw.Namespace, "uid": "namespace-uid", "resourceVersion": "1", "labels": labels, "annotations": annotations}}
				meta := object{"name": name, "namespace": gw.Namespace, "uid": "account-uid", "resourceVersion": "1", "labels": labels}
				account = object{"apiVersion": "v1", "kind": "ServiceAccount", "metadata": meta, "automountServiceAccountToken": false}
				switch mode {
				case "foreign":
					meta["labels"] = object{}
				case "deleting":
					meta["deletionTimestamp"] = "2026-09-17T00:00:00Z"
				case "automatic token":
					account["automountServiceAccountToken"] = true
				case "missing annotation":
					delete(annotations, "stego.dev/service-account-"+workload)
				}
				var err error
				if workload == "gateway" {
					err = k.Ensure(context.Background(), gw, release, 1)
				} else {
					err = k.EnsureConsole(context.Background(), gw, 1, 65532)
				}
				if err == nil {
					t.Fatal("workload accepted an account that was not ready")
				}
				pending := mode == "missing" || mode == "deleting"
				if errors.Is(err, ErrPending) != pending {
					t.Fatal("account readiness result differs", err)
				}
				expected := 2
				if mode == "missing annotation" {
					expected = 1
				}
				if reads != expected {
					t.Fatal("unexpected account check requests", reads)
				}
			})
		}
	}
}
