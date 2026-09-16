//go:build ignore

// This command removes only allocations from a dedicated browser test fixture.
// STEGO performs each deletion and preserves its ownership and UID checks.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jsell-rh/hypershell-stego/out/deploy/allocation"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

type cleanupTarget struct{ Profile, Namespace, Owner string }

func cleanupSnapshot(ctx context.Context, client *kube.Client, collection, marker string) ([]kube.Object, error) {
	query := url.Values{"labelSelector": {allocation.MarkerLabel + "=" + marker}, "limit": {"64"}}
	page, code, err := client.Request(ctx, http.MethodGet, collection+"?"+query.Encode(), nil)
	if err != nil {
		return nil, errors.New("allocation inventory read failed")
	}
	items, ok := kube.Nested(page, "items").([]any)
	if code != 200 || !ok || len(items) > 64 || kube.String(page, "metadata", "continue") != "" || kube.String(page, "metadata", "resourceVersion") == "" {
		return nil, errors.New("allocation inventory is incomplete or exceeds its limit")
	}
	result := make([]kube.Object, 0, len(items))
	seen := map[string]bool{}
	for _, value := range items {
		object, ok := value.(map[string]any)
		if !ok || kube.String(object, "metadata", "uid") == "" || kube.String(object, "metadata", "resourceVersion") == "" || kube.String(object, "metadata", "labels", allocation.MarkerLabel) != marker {
			return nil, errors.New("allocation inventory has an invalid identity")
		}
		name := kube.String(object, "metadata", "name")
		if name == "" || seen[name] {
			return nil, errors.New("allocation inventory has a repeated or empty name")
		}
		seen[name] = true
		result = append(result, kube.Object(object))
	}
	return result, nil
}

func cleanupTargets(namespace string, namespaces, bindings []kube.Object) ([]cleanupTarget, error) {
	result := map[string]cleanupTarget{}
	add := func(object kube.Object, name string) error {
		target := cleanupTarget{kube.String(object, "metadata", "labels", allocation.ProfileLabel), name, kube.String(object, "metadata", "labels", "hypershell.redhat.io/gateway-id")}
		if !regexp.MustCompile(`^[0-9A-Za-z]{27}$`).MatchString(target.Owner) || kube.String(object, "metadata", "labels", "app.kubernetes.io/managed-by") != "hypershell-gateway-controller" {
			return errors.New("test allocation has a different owner")
		}
		pattern := `^openshell-[0-9a-f]{16}$`
		if target.Profile == "gateway-state" {
			pattern = `^openshell-state-[0-9a-f]{40}$`
		} else if target.Profile == "gateway-console-state" {
			pattern = `^openshell-console-[0-9a-f]{40}$`
		} else if target.Profile != "gateway" {
			return errors.New("test allocation has an unknown profile")
		}
		if !regexp.MustCompile(pattern).MatchString(name) {
			return errors.New("test allocation has an invalid namespace")
		}
		if old, ok := result[name]; ok && old != target {
			return errors.New("test allocation identities conflict")
		}
		result[name] = target
		if len(result) > 8 {
			return errors.New("test cleanup exceeds eight allocations")
		}
		return nil
	}
	for _, object := range namespaces {
		if err := add(object, kube.String(object, "metadata", "name")); err != nil {
			return nil, err
		}
	}
	prefix := namespace + ".hypershell-namespace-allocation."
	for _, object := range bindings {
		name := kube.String(object, "metadata", "name")
		if !strings.HasPrefix(name, prefix) {
			return nil, errors.New("test binding has a foreign name")
		}
		suffix := strings.TrimPrefix(name, prefix)
		dot := strings.LastIndexByte(suffix, '.')
		if dot < 1 {
			return nil, errors.New("test binding has no namespace")
		}
		index, err := strconv.Atoi(suffix[dot+1:])
		if err != nil || index < 0 || index > 15 || strconv.Itoa(index) != suffix[dot+1:] {
			return nil, errors.New("test binding has an invalid index")
		}
		if err := add(object, suffix[:dot]); err != nil {
			return nil, err
		}
	}
	targets := make([]cleanupTarget, 0, len(result))
	for _, target := range result {
		targets = append(targets, target)
	}
	// Remove workloads before retained state. No cleanup bypasses finalizers.
	sort.Slice(targets, func(i, j int) bool {
		if targets[i].Profile != targets[j].Profile {
			return targets[i].Profile < targets[j].Profile
		}
		return targets[i].Namespace < targets[j].Namespace
	})
	return targets, nil
}

func runCleanup() error {
	namespace := flag.String("namespace", "", "Dedicated test control namespace")
	server := flag.String("server", "", "Verified Kubernetes API URL")
	ca := flag.String("ca-file", "", "Private CA file, or empty for system trust")
	token := flag.String("token-file", "", "Private CI bearer token file")
	result := flag.String("result", "", "Public cleanup result file")
	remove := flag.Bool("remove", false, "Remove observed test allocations")
	flag.Parse()
	if flag.NArg() != 0 || !regexp.MustCompile(`^stego-service-[a-z0-9-]{1,40}$`).MatchString(*namespace) || *result == "" {
		return errors.New("invalid test cleanup arguments")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()
	caller, err := kube.New(kube.Options{ServerURL: *server, CAFile: *ca, TokenFile: *token})
	if err != nil {
		return errors.New("CI connection setup failed")
	}
	defer caller.Close()
	requested, code, err := caller.Request(ctx, http.MethodPost, "/api/v1/namespaces/"+*namespace+"/serviceaccounts/hypershell-namespace-allocation/token", kube.Object{"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest", "spec": kube.Object{"expirationSeconds": 600}})
	value := kube.String(requested, "status", "token")
	if err != nil || code != 201 || value == "" {
		return errors.New("allocator token request failed")
	}
	directory, err := os.MkdirTemp("", "stego-cleanup-token-")
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
	allocator, err := allocation.New(client, *namespace)
	if err != nil {
		return err
	}
	inventory := func() ([]cleanupTarget, error) {
		namespaces, err := cleanupSnapshot(ctx, client, "/api/v1/namespaces", allocator.Marker())
		if err != nil {
			return nil, err
		}
		bindings, err := cleanupSnapshot(ctx, client, "/apis/rbac.authorization.k8s.io/v1/clusterrolebindings", allocator.Marker())
		if err != nil {
			return nil, err
		}
		return cleanupTargets(*namespace, namespaces, bindings)
	}
	targets, err := inventory()
	if err != nil {
		return err
	}
	if len(targets) > 0 && !*remove {
		return errors.New("old test allocations remain; cleanup is required")
	}
	for _, target := range targets {
		for {
			done, err := allocator.Delete(ctx, target.Profile, target.Namespace, target.Owner)
			if err != nil {
				return errors.New("generated allocation cleanup failed")
			}
			if done {
				break
			}
			select {
			case <-ctx.Done():
				return errors.New("allocation cleanup deadline reached")
			case <-time.After(time.Second):
			}
		}
	}
	remaining, err := inventory()
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return errors.New("allocation resources remain")
	}
	record := struct {
		AllocationsBefore int  `json:"allocations_before"`
		AllocationsAbsent bool `json:"allocations_absent"`
	}{len(targets), true}
	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(*result, append(data, '\n'), 0600); err != nil {
		return err
	}
	return nil
}
func main() {
	if err := runCleanup(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
