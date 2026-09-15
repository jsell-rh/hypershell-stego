//go:build ignore

// This diagnostic counts policy writes from the generated allocator against a
// TLS API fixture. It makes no cluster request and does not test CNI enforcement.
package main

import (
	"context"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	allocation "github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func main() { os.Exit(run()) }
func run() int {
	must(os.Setenv("STEGO_ALLOCATION_NETWORK_ENDPOINTS", `{"kubernetes":["192.0.2.1:443"]}`))
	flags := flag.NewFlagSet("allocation-network-audit", flag.ContinueOnError)
	extra := flags.Bool("additional-policy", false, "Add an unlabelled allow-all policy to the test fixture")
	if flags.Parse(os.Args[1:]) != nil || flags.NArg() != 0 {
		return 2
	}
	injected := false
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
				limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
				if err != nil || limit < 1 || limit > 64 {
					http.Error(w, "invalid limit", 400)
					return
				}
				keys := []string{}
				for key, value := range objects {
					if !strings.HasPrefix(key, r.URL.Path+"/") || strings.Contains(strings.TrimPrefix(key, r.URL.Path+"/"), "/") {
						continue
					}
					match := true
					if selector := r.URL.Query().Get("labelSelector"); selector != "" {
						for _, term := range strings.Split(selector, ",") {
							label, want, ok := strings.Cut(term, "=")
							if !ok || kube.String(value, "metadata", "labels", label) != want {
								match = false
								break
							}
						}
					}
					if match {
						keys = append(keys, key)
					}
				}
				sort.Strings(keys)
				meta := kube.Object{"resourceVersion": "1"}
				if len(keys) > limit {
					keys = keys[:limit]
					meta["continue"] = "remaining"
				}
				items := []any{}
				for _, key := range keys {
					items = append(items, objects[key])
				}
				json.NewEncoder(w).Encode(kube.Object{"apiVersion": "v1", "kind": "List", "metadata": meta, "items": items})
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
			if *extra {
				objects[r.URL.Path+"/allow-all"] = kube.Object{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": kube.Object{"name": "allow-all", "namespace": meta["namespace"], "uid": "unexpected-policy", "resourceVersion": "1"}, "spec": kube.Object{"podSelector": kube.Object{}, "policyTypes": []string{"Ingress", "Egress"}, "ingress": []any{kube.Object{}}, "egress": []any{kube.Object{}}}}
				injected = true
			}
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
	result := map[string]any{"scope": "Generated allocator against a TLS API fixture; no real cluster", "ensure_succeeded": err == nil, "network_policy_writes": policies, "additional_policy_injected": injected, "writes": writes}
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
