//go:build ignore

// This diagnostic counts policy writes from the generated allocator against a
// TLS API fixture. It makes no cluster request and does not test CNI enforcement.
package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	allocation "github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func main() { os.Exit(run()) }
func run() int {
	directory, err := os.MkdirTemp("", "allocation-network-input-")
	must(err)
	defer os.RemoveAll(directory)
	objects := map[string]kube.Object{}
	var mutex sync.Mutex
	writes := []string{}
	policies := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mutex.Lock()
		defer mutex.Unlock()
		if r.Header.Get("Authorization") != "Bearer fixture" {
			http.Error(w, "denied", 401)
			return
		}
		if r.Method == http.MethodGet {
			if r.URL.Query().Get("limit") != "" {
				json.NewEncoder(w).Encode(kube.Object{"apiVersion": "v1", "kind": "List", "metadata": kube.Object{"resourceVersion": "1"}, "items": []any{}})
				return
			}
			value, ok := objects[r.URL.Path]
			if !ok {
				w.WriteHeader(404)
				return
			}
			json.NewEncoder(w).Encode(value)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "unsupported", 405)
			return
		}
		var value kube.Object
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&value) != nil {
			http.Error(w, "body", 400)
			return
		}
		if r.URL.Query().Get("fieldValidation") != "Strict" {
			http.Error(w, "strict validation required", 400)
			return
		}
		meta, ok := value["metadata"].(map[string]any)
		if !ok {
			http.Error(w, "metadata", 400)
			return
		}
		name, _ := meta["name"].(string)
		if name == "" {
			http.Error(w, "name", 400)
			return
		}
		meta["uid"] = fmt.Sprintf("00000000-0000-4000-8000-%012d", len(writes)+1)
		meta["resourceVersion"] = "1"
		kind, _ := value["kind"].(string)
		writes = append(writes, kind+" "+r.URL.Path+"/"+name)
		if kind == "NetworkPolicy" {
			policies++
		}
		objects[r.URL.Path+"/"+name] = value
		w.WriteHeader(201)
		json.NewEncoder(w).Encode(value)
	}))
	defer server.Close()
	certificate, err := x509.ParseCertificate(server.TLS.Certificates[0].Certificate[0])
	must(err)
	ca := filepath.Join(directory, "ca.pem")
	token := filepath.Join(directory, "token")
	must(os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certificate.Raw}), 0600))
	must(os.WriteFile(token, []byte("fixture"), 0600))
	client, err := kube.New(kube.Options{ServerURL: server.URL, CAFile: ca, TokenFile: token})
	must(err)
	defer client.Close()
	allocator, err := allocation.New(client, "control")
	must(err)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	err = allocator.Ensure(ctx, "gateway", "openshell-"+strings.Repeat("a", 16), "00000000-0000-4000-8000-000000000123")
	mutex.Lock()
	result := map[string]any{"scope": "Generated allocator against a TLS API fixture; no real cluster", "ensure_succeeded": err == nil, "network_policy_writes": policies, "writes": writes}
	if err != nil {
		result["error_type"] = "allocator failure"
	}
	observedPolicies := policies
	data, encodeErr := json.MarshalIndent(result, "", "  ")
	mutex.Unlock()
	must(encodeErr)
	fmt.Println(string(data))
	if err != nil {
		return 2
	}
	if observedPolicies == 0 {
		return 1
	}
	return 0
}
func must(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "Audit setup failed")
		os.Exit(2)
	}
}
