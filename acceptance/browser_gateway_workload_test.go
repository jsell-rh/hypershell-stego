package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/databasecontroller"
	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	keycloak "github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

type allocationTarget struct{ profile, id string }

type browserGatewayWorkload struct {
	t           *testing.T
	p           *kubernetesBrowser
	f           *fixture
	identity    *keycloakFixture
	owner       *consoleBrowser
	kubernetes  *kube.Client
	options     databasecontroller.KubernetesOptions
	tokens      map[string]string
	allocations map[string]allocationTarget
	call        gatewayCall
	stops       []func()
	outputs     []func() string
	telemetry   []string
	restarts    []func()
}

func prepareBrowserGatewayWorkload(t *testing.T, p *kubernetesBrowser, f *fixture, k *keycloakFixture, settings []string) (*browserGatewayWorkload, []string) {
	t.Helper()
	issuer := os.Getenv("STEGO_TEST_GATEWAY_CLUSTER_ISSUER")
	if issuer == "" {
		t.Fatal("Gateway workflow requires an existing test ClusterIssuer")
	}
	tokenFile := filepath.Join(t.TempDir(), "kubernetes-token")
	data, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		t.Fatal("Kubernetes test token unavailable")
	}
	if err := os.WriteFile(tokenFile, data, 0600); err != nil {
		t.Fatal(err)
	}
	clear(data)
	options := databasecontroller.KubernetesOptions{ServerURL: "https://kubernetes.default.svc", CAFile: "/var/run/secrets/kubernetes.io/serviceaccount/ca.crt", TokenFile: tokenFile, ClusterIssuer: issuer}
	client, err := kube.New(kube.Options{ServerURL: options.ServerURL, CAFile: options.CAFile, TokenFile: tokenFile})
	if err != nil {
		t.Fatal("Kubernetes client setup failed")
	}
	w := &browserGatewayWorkload{t: t, p: p, f: f, identity: k, kubernetes: client, options: options, tokens: map[string]string{}, telemetry: browserWorkerTelemetry(settings), allocations: map[string]allocationTarget{}}
	t.Cleanup(func() {
		for i := len(w.stops) - 1; i >= 0; i-- {
			w.stops[i]()
		}
		allocator, err := allocation.New(client, p.namespace)
		if err != nil {
			t.Error(err)
			return
		}
		for ns, target := range w.allocations {
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			for {
				gone, err := allocator.Delete(ctx, target.profile, ns, target.id)
				if err != nil {
					t.Error("allocation cleanup failed", ns, err)
					break
				}
				if gone {
					break
				}
				select {
				case <-ctx.Done():
					t.Error("allocation cleanup timed out", ns)
				case <-time.After(time.Second):
					continue
				}
				break
			}
			cancel()
		}
		client.Close()
	})
	// These are declared placement inputs. Controllers must supply observations.
	if _, err := f.db.Exec("UPDATE gateway_releases SET image=$1 WHERE id=$2", gatewayImage, f.release); err != nil {
		t.Fatal(err)
	}
	settings = append(settings, "DATABASE_PROVIDER=deployment")
	var subjects []string
	ids := map[string]string{}
	for _, name := range []string{"database", "identity", "workload", "allocation"} {
		username := "browser-controller-" + name
		subject := k.human(t, username)
		subjects = append(subjects, subject)
		ids[name] = subject
		w.tokens[name] = k.browserLogin(t, "hypershell", username)

	}
	settings = withControllerWriteGrants(t, settings, databaseWriteGrant(ids["database"], f.cluster), writeGrant(ids["identity"], "configure.identity", ""), writeGrant(ids["workload"], "observe.workload", f.cluster))
	settings = withCleanupGrants(t, settings, cleanupGrant(ids["database"], "ManagedDatabase", "provider", f.cluster), cleanupGrant(ids["workload"], "ManagedDatabase", "record", f.cluster), cleanupGrant(ids["identity"], "Gateway", "identity", ""), cleanupGrant(ids["workload"], "Gateway", "workload", f.cluster))
	encoded, _ := json.Marshal(subjects)
	settings = append(settings, "HYPERSHELL_CONTROL_PLANE_SUBJECTS="+string(encoded))
	return w, settings
}

func (w *browserGatewayWorkload) trackAllocation(name, id, profile string) {
	w.t.Helper()
	w.allocations[name] = allocationTarget{profile: profile, id: id}
}

func (w *browserGatewayWorkload) start(owner *consoleBrowser, address, ca, gatewayID string) {
	w.t.Helper()
	w.owner = owner
	rows, err := w.f.db.Query("SELECT g.id,g.namespace,d.id,d.namespace FROM gateways g JOIN managed_databases d ON d.id=g.database_id WHERE g.cluster_id=$1 AND g.deleted_at IS NULL", w.f.cluster)
	if err != nil {
		w.t.Fatal(err)
	}
	type placement struct{ id, namespace, database, databaseNamespace string }
	var placements []placement
	for rows.Next() {
		var p placement
		if err := rows.Scan(&p.id, &p.namespace, &p.database, &p.databaseNamespace); err != nil {
			rows.Close()
			w.t.Fatal(err)
		}
		placements = append(placements, p)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(placements) < 1 || len(placements) > 2 {
		w.t.Fatal("unexpected browser Gateway placement count")
	}
	for _, p := range placements {
		w.trackAllocation(p.databaseNamespace, p.database, "database")
		w.trackAllocation(p.namespace, p.id, "gateway")
	}
	w.startWorkers(address, ca)
	w.check(gatewayID)
	w.checkRPC(gatewayID)
	w.checkAllocationAccess()
	for _, restart := range w.restarts {
		restart()
	}
	w.check(gatewayID)
}

func (w *browserGatewayWorkload) check(id string) {
	w.t.Helper()
	deadline := time.Now().Add(180 * time.Second)
	for {
		response := w.owner.api(w.t, "GET", "/gateways/"+id, nil)
		var gateway httpapi.Gateway
		if response.StatusCode == 200 && json.Unmarshal(response.Body, &gateway) == nil && gateway.Status != nil && *gateway.Status == "Healthy" && gateway.Phase != nil && *gateway.Phase == "Running" {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			object, code, err := w.kubernetes.Request(ctx, http.MethodGet, "/apis/apps/v1/namespaces/"+gateway.Namespace+"/deployments/openshell-gateway", nil)
			cancel()
			var state struct {
				Metadata struct{ Generation int64 }
				Status   struct {
					ObservedGeneration             int64
					ReadyReplicas, UpdatedReplicas int
				}
			}
			data, _ := json.Marshal(object)
			if err == nil && code == 200 && json.Unmarshal(data, &state) == nil && state.Metadata.Generation > 0 && state.Status.ObservedGeneration >= state.Metadata.Generation && state.Status.ReadyReplicas == 1 && state.Status.UpdatedReplicas == 1 {
				w.t.Log("Browser Gateway has current controller observations and a ready OpenShell Deployment")
				return
			}
		}
		if time.Now().After(deadline) {
			for _, logs := range w.outputs {
				w.t.Log(logs())
			}
			w.t.Fatal("browser Gateway did not complete real provisioning")
		}
		time.Sleep(time.Second)
	}
}

func (w *browserGatewayWorkload) audience(id string) string {
	w.t.Helper()
	value, err := keycloak.GatewayClientID(id)
	if err != nil {
		w.t.Fatal(err)
	}
	return value
}
