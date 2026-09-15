//go:build ignore

// Create two generated allocations for the bounded CI cleanup check.
package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

func seedRecovery() error {
	server := flag.String("server", "", "Verified Kubernetes API URL")
	ca := flag.String("ca-file", "", "Private CA file, or empty for system trust")
	token := flag.String("token-file", "", "Private CI bearer token file")
	result := flag.String("result", "", "Public fixture record")
	flag.Parse()
	if flag.NArg() != 0 || *result == "" {
		return errors.New("invalid recovery fixture arguments")
	}
	const namespace = "stego-service-ci"
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	caller, err := kube.New(kube.Options{ServerURL: *server, CAFile: *ca, TokenFile: *token})
	if err != nil {
		return errors.New("CI connection setup failed")
	}
	defer caller.Close()
	requested, code, err := caller.Request(ctx, http.MethodPost, "/api/v1/namespaces/"+namespace+"/serviceaccounts/hypershell-namespace-allocation/token", kube.Object{"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest", "spec": kube.Object{"expirationSeconds": 600}})
	value := kube.String(requested, "status", "token")
	if err != nil || code != 201 || value == "" {
		return errors.New("allocator token request failed")
	}
	directory, err := os.MkdirTemp("", "stego-recovery-token-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(directory)
	path := filepath.Join(directory, "token")
	if err = os.WriteFile(path, []byte(value), 0600); err != nil {
		return err
	}
	client, err := kube.New(kube.Options{ServerURL: *server, CAFile: *ca, TokenFile: path})
	if err != nil {
		return errors.New("allocator connection setup failed")
	}
	defer client.Close()
	allocator, err := allocation.New(client, namespace)
	if err != nil {
		return err
	}
	var random [12]byte
	if _, err = rand.Read(random[:]); err != nil {
		return err
	}
	owner := "CI0" + hex.EncodeToString(random[:])
	digest := sha256.Sum256([]byte(owner))
	suffix := hex.EncodeToString(digest[:])
	targets := []struct{ Profile, Namespace, Owner string }{
		{"gateway", "openshell-" + suffix[:16], owner},
		{"gateway-state", "openshell-state-" + suffix[:40], owner},
	}
	// Record the intended identities before the first write. Cleanup reads the
	// actual allocator inventory, so partial creation is also covered.
	data, err := json.MarshalIndent(targets, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(*result, append(data, '\n'), 0600); err != nil {
		return err
	}
	for _, target := range targets {
		if err = allocator.Ensure(ctx, target.Profile, target.Namespace, target.Owner); err != nil {
			return errors.New("generated recovery allocation failed")
		}
	}
	return nil
}

func main() {
	if err := seedRecovery(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
