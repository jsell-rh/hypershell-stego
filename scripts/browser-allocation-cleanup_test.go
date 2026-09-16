//go:build ignore

package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func cleanupObject(name, profile, owner string) kube.Object {
	return kube.Object{"metadata": kube.Object{"name": name, "uid": "uid-" + name, "resourceVersion": "1", "labels": kube.Object{"stego.dev/allocator": "marker", "stego.dev/allocation-profile": profile, "hypershell.redhat.io/gateway-id": owner, "app.kubernetes.io/managed-by": "hypershell-gateway-controller"}}}
}
func TestCleanupTargetsKeepOrphanBindingsAndOrder(t *testing.T) {
	gateway, state, owner := "openshell-0123456789abcdef", "openshell-state-0123456789abcdef0123456789abcdef01234567", "0123456789abcdefghijklmnopq"
	targets, err := cleanupTargets("stego-service-ci", []kube.Object{cleanupObject(state, "gateway-state", owner)}, []kube.Object{cleanupObject("stego-service-ci.hypershell-namespace-allocation."+gateway+".2", "gateway", owner)})
	want := []cleanupTarget{{"gateway", gateway, owner}, {"gateway-state", state, owner}}
	if err != nil || !reflect.DeepEqual(targets, want) {
		t.Fatal("orphan inventory or deletion order differs", err)
	}
}
func TestCleanupTargetsIncludeConsoleStateAfterWorkload(t *testing.T) {
	gateway, console, owner := "openshell-0123456789abcdef", "openshell-console-0123456789abcdef0123456789abcdef01234567", "0123456789abcdefghijklmnopq"
	targets, err := cleanupTargets("stego-service-ci", []kube.Object{cleanupObject(console, "gateway-console-state", owner), cleanupObject(gateway, "gateway", owner)}, nil)
	want := []cleanupTarget{{"gateway", gateway, owner}, {"gateway-console-state", console, owner}}
	if err != nil || !reflect.DeepEqual(targets, want) {
		t.Fatal("console state cleanup order differs", err)
	}
	for _, invalid := range []string{gateway, "openshell-state-0123456789abcdef0123456789abcdef01234567", console + "0"} {
		if _, err := cleanupTargets("stego-service-ci", []kube.Object{cleanupObject(invalid, "gateway-console-state", owner)}, nil); err == nil {
			t.Fatal("console cleanup accepted another namespace shape")
		}
	}
}

func TestCleanupTargetsRejectForeignAndConflictingResources(t *testing.T) {
	name, owner := "openshell-0123456789abcdef", "0123456789abcdefghijklmnopq"
	cases := []struct{ namespaces, bindings []kube.Object }{
		{namespaces: []kube.Object{cleanupObject("production", "gateway", owner)}},
		{namespaces: []kube.Object{cleanupObject(name, "unknown", owner)}},
		{namespaces: []kube.Object{cleanupObject(name, "gateway", "invalid")}},
		{namespaces: []kube.Object{cleanupObject(name, "gateway", owner), cleanupObject(name, "gateway", "1123456789abcdefghijklmnopq")}},
		{bindings: []kube.Object{cleanupObject("other.hypershell-namespace-allocation."+name+".2", "gateway", owner)}},
		{bindings: []kube.Object{cleanupObject("stego-service-ci.hypershell-namespace-allocation."+name+".02", "gateway", owner)}},
		{bindings: []kube.Object{cleanupObject("stego-service-ci.hypershell-namespace-allocation."+name+".16", "gateway", owner)}},
	}
	foreign := cleanupObject(name, "gateway", owner)
	foreign["metadata"].(kube.Object)["labels"].(kube.Object)["app.kubernetes.io/managed-by"] = "other"
	cases = append(cases, struct{ namespaces, bindings []kube.Object }{namespaces: []kube.Object{foreign}})
	for _, test := range cases {
		if targets, err := cleanupTargets("stego-service-ci", test.namespaces, test.bindings); err == nil || targets != nil {
			t.Fatal("invalid cleanup inventory was accepted")
		}
	}
}
func TestCleanupTargetsRejectOversizedInventory(t *testing.T) {
	objects := []kube.Object{}
	for _, name := range []string{"0000000000000000", "0000000000000001", "0000000000000002", "0000000000000003", "0000000000000004", "0000000000000005", "0000000000000006", "0000000000000007", "0000000000000008"} {
		objects = append(objects, cleanupObject("openshell-"+name, "gateway", "0123456789abcdefghijklmnopq"))
	}
	if targets, err := cleanupTargets("stego-service-ci", objects, nil); err == nil || targets != nil {
		t.Fatal("cleanup accepted more than eight allocations")
	}
}
func TestCleanupSnapshotRequiresCompleteVerifiedInventory(t *testing.T) {
	for _, test := range []struct {
		name   string
		mutate func(kube.Object)
		valid  bool
	}{
		{name: "complete", valid: true, mutate: func(kube.Object) {}},
		{name: "continued", mutate: func(p kube.Object) { p["metadata"].(kube.Object)["continue"] = "next" }},
		{name: "no-version", mutate: func(p kube.Object) { delete(p["metadata"].(kube.Object), "resourceVersion") }},
		{name: "foreign-marker", mutate: func(p kube.Object) {
			p["items"].([]any)[0].(kube.Object)["metadata"].(kube.Object)["labels"].(kube.Object)["stego.dev/allocator"] = "other"
		}},
		{name: "no-uid", mutate: func(p kube.Object) { delete(p["items"].([]any)[0].(kube.Object)["metadata"].(kube.Object), "uid") }},
		{name: "repeated-name", mutate: func(p kube.Object) { p["items"] = append(p["items"].([]any), p["items"].([]any)[0]) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			page := kube.Object{"metadata": kube.Object{"resourceVersion": "7"}, "items": []any{cleanupObject("openshell-0123456789abcdef", "gateway", "0123456789abcdefghijklmnopq")}}
			test.mutate(page)
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" || r.URL.Path != "/api/v1/namespaces" || r.URL.Query().Get("limit") != "64" || r.URL.Query().Get("labelSelector") != "stego.dev/allocator=marker" || r.Header.Get("Authorization") != "Bearer test-token" {
					t.Error("unexpected inventory request")
				}
				w.Header().Set("Content-Type", "application/json")
				json.NewEncoder(w).Encode(page)
			}))
			defer server.Close()
			directory := t.TempDir()
			ca, token := filepath.Join(directory, "ca.pem"), filepath.Join(directory, "token")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(token, []byte("test-token"), 0600); err != nil {
				t.Fatal(err)
			}
			client, err := kube.New(kube.Options{ServerURL: server.URL, CAFile: ca, TokenFile: token})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			objects, err := cleanupSnapshot(context.Background(), client, "/api/v1/namespaces", "marker")
			if test.valid {
				if err != nil || len(objects) != 1 {
					t.Fatal("complete inventory failed", err)
				}
			} else if err == nil || objects != nil {
				t.Fatal("incomplete inventory was accepted")
			}
		})
	}
}
