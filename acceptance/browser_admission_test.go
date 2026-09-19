package acceptance

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	transport "github.com/jsell-rh/hypershell-stego/out/application/client"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// These dry-run bodies contain only public allocation data. Read their bounded
// Status response to prove the expected policy denied them. Production clients
// must continue to omit API response bodies from errors.
func (w *browserGatewayWorkload) checkAdmission(ctx context.Context, state, gateway, marker, token string) {
	w.t.Helper()
	if token == "" {
		w.t.Fatal("allocator test identity is unavailable")
	}
	client, err := transport.New(transport.Options{BaseURL: w.options.ServerURL, CAFile: w.options.CAFile})
	if err != nil {
		w.t.Fatal("admission test client setup failed")
	}
	defer client.Close()
	namespace, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+state, nil)
	fingerprint := kube.String(namespace, "metadata", "annotations", "hypershell.redhat.io/state-identity")
	if err != nil || code != 200 || len(fingerprint) != 64 {
		w.t.Fatal("retained state fingerprint is unavailable")
	}
	type rule struct{ policy, message string }
	probes := []struct {
		name, method, path string
		body               kube.Object
		rules              []rule
	}{
		{"foreign namespace", "POST", "/api/v1/namespaces?dryRun=All&fieldValidation=Strict", kube.Object{"apiVersion": "v1", "kind": "Namespace", "metadata": kube.Object{"name": "stego-denied-namespace"}}, []rule{{"allocation", "Namespace must match its allocation profile"}}},
		{"quota change", "PATCH", "/api/v1/namespaces/" + state + "/resourcequotas/stego-allocation?dryRun=All&fieldValidation=Strict", kube.Object{"spec": kube.Object{"hard": kube.Object{"pods": "3"}}}, []rule{{"allocation", "Quota must match its allocation profile"}}},
		{"allocation identity change", "PATCH", "/api/v1/namespaces/" + state + "?dryRun=All&fieldValidation=Strict", kube.Object{"metadata": kube.Object{"labels": kube.Object{"stego.dev/allocation-profile": "gateway"}}}, []rule{{"ownership", "Allocation identity and Pod security are immutable"}, {"allocation", "Allocator cannot change resource ownership"}}},
		{"retained fingerprint change", "PATCH", "/api/v1/namespaces/" + state + "?dryRun=All&fieldValidation=Strict", kube.Object{"metadata": kube.Object{"annotations": kube.Object{"hypershell.redhat.io/state-identity": "changed-public-fingerprint"}}}, []rule{{"ownership", "Allocation identity and Pod security are immutable"}}},
		{"retained fingerprint removal", "PATCH", "/api/v1/namespaces/" + state + "?dryRun=All&fieldValidation=Strict", kube.Object{"metadata": kube.Object{"annotations": kube.Object{"hypershell.redhat.io/state-identity": nil}}}, []rule{{"ownership", "Allocation identity and Pod security are immutable"}}},
		{"foreign inspection subject", "PATCH", "/apis/rbac.authorization.k8s.io/v1/namespaces/" + gateway + "/rolebindings/stego-" + marker + "-5?dryRun=All&fieldValidation=Strict", kube.Object{"subjects": []any{kube.Object{"kind": "ServiceAccount", "name": "service-check", "namespace": "default"}}}, []rule{{"allocation", "Binding must match its allocated namespace"}}},
	}
	type result struct {
		Name   string
		Code   int
		Policy string
	}
	var results []result
	for _, probe := range probes {
		body, err := json.Marshal(probe.body)
		if err != nil {
			w.t.Fatal(err)
		}
		contentType := "application/json"
		if probe.method == "PATCH" {
			contentType = "application/merge-patch+json"
		}
		response, err := client.Do(ctx, probe.method, probe.path, http.Header{"Authorization": {"Bearer " + token}, "Content-Type": {contentType}, "Accept": {"application/json"}}, body)
		if err != nil {
			w.t.Fatal("admission dry-run transport failed")
		}
		var state struct {
			Kind, Status, Reason, Message string
			Code                          int
		}
		if json.Unmarshal(response.Body, &state) != nil || state.Kind != "Status" || state.Status != "Failure" || state.Code != response.StatusCode || !((state.Code == 422 && state.Reason == "Invalid") || (state.Code == 403 && state.Reason == "Forbidden")) {
			w.t.Fatal("allocation dry-run did not return a policy denial", probe.name, response.StatusCode)
		}
		matched := ""
		for _, rule := range probe.rules {
			policy := w.p.namespace + ".hypershell-namespace-allocation." + rule.policy
			if strings.Contains(state.Message, "ValidatingAdmissionPolicy '"+policy+"'") && strings.Contains(state.Message, rule.message) {
				matched = policy
				break
			}
		}
		if matched == "" {
			w.t.Fatal("allocation dry-run was not denied by the expected policy", probe.name, state.Code)
		}
		results = append(results, result{probe.name, state.Code, matched})
	}
	// Confirm that each public probe left the stored objects unchanged.
	if _, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/stego-denied-namespace", nil); err != nil || code != 404 {
		w.t.Fatal("dry-run created a foreign namespace", code)
	}
	quota, code, err := w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+state+"/resourcequotas/stego-allocation", nil)
	if err != nil || code != 200 || kube.String(quota, "spec", "hard", "pods") != "0" {
		w.t.Fatal("dry-run changed the allocation quota", code)
	}
	namespace, code, err = w.kubernetes.Request(ctx, "GET", "/api/v1/namespaces/"+state, nil)
	if err != nil || code != 200 || kube.String(namespace, "metadata", "labels", "stego.dev/allocation-profile") != "gateway-state" || kube.String(namespace, "metadata", "annotations", "hypershell.redhat.io/state-identity") != fingerprint {
		w.t.Fatal("dry-run changed allocation identity", code)
	}
	if dir := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); dir != "" {
		data, err := json.MarshalIndent(results, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(dir, "allocation-admission.json"), data, 0600) != nil {
			w.t.Fatal("cannot write admission evidence")
		}
	}
	w.t.Log("Six server dry-runs were denied by the expected generated admission rules; stored namespace, quota, and fingerprint remained unchanged")
}
