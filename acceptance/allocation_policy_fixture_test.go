package acceptance

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// Capture the generated policy for a protocol fixture. This setup stops before
// access bindings. The count worker must still read and verify the policy.
func allocationPolicyFixture(t *testing.T, control, profile, namespace, owner string) kube.Object {
	t.Helper()
	objects := map[string]kube.Object{}
	var policy kube.Object
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer allocation-policy-fixture" {
			w.WriteHeader(403)
			return
		}
		if r.Method == "GET" {
			if strings.HasSuffix(r.URL.Path, "/networkpolicies") {
				_ = json.NewEncoder(w).Encode(kube.Object{"metadata": kube.Object{"resourceVersion": "1"}, "items": []any{policy}})
				return
			}
			if object, exists := objects[r.URL.Path]; exists {
				_ = json.NewEncoder(w).Encode(object)
			} else {
				w.WriteHeader(404)
			}
			return
		}
		if r.Method != "POST" {
			w.WriteHeader(403)
			return
		}
		var object kube.Object
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 65536)).Decode(&object) != nil {
			w.WriteHeader(400)
			return
		}
		kind := kube.String(object, "kind")
		if kind != "Namespace" && kind != "ResourceQuota" && kind != "NetworkPolicy" {
			w.WriteHeader(403)
			return
		}
		meta := object["metadata"].(map[string]any)
		meta["uid"], meta["resourceVersion"] = "fixture-"+kind, "1"
		objects[r.URL.Path+"/"+kube.String(object, "metadata", "name")] = object
		if kind == "NetworkPolicy" {
			policy = object
		}
		w.WriteHeader(201)
		_ = json.NewEncoder(w).Encode(object)
	}))
	defer server.Close()
	directory := t.TempDir()
	ca, token := filepath.Join(directory, "ca"), filepath.Join(directory, "token")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte("allocation-policy-fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	client, err := kube.New(kube.Options{ServerURL: server.URL, CAFile: ca, TokenFile: token})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	allocator, err := allocation.New(client, control)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err = allocator.Ensure(ctx, profile, namespace, owner); err == nil || policy == nil {
		t.Fatal("policy fixture did not stop before access bindings", err)
	}
	return policy
}

func TestAllocationPolicyFixtureCapturesGeneratedPolicy(t *testing.T) {
	bindings := os.Getenv("STEGO_TEST_ALLOCATION_NETWORK_ENDPOINTS")
	if bindings == "" {
		bindings = `{"kubernetes":["192.0.2.1:443"]}`
	}
	t.Setenv("STEGO_ALLOCATION_NETWORK_ENDPOINTS", bindings)
	policy := allocationPolicyFixture(t, "count-control", "gateway", "openshell-aaaaaaaaaaaaaaaa", "owner-1")
	if kube.String(policy, "kind") != "NetworkPolicy" || kube.String(policy, "metadata", "uid") == "" || kube.String(policy, "metadata", "annotations", "stego.dev/network-spec-sha256") == "" {
		t.Fatal("protocol fixture has no generated policy identity")
	}
}
