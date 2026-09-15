package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// The test actor must not install or change cluster permissions. Its token
// requests must also stay in the fixture's control namespace.
func (w *browserGatewayWorkload) checkInstallationAccess(state, gateway string) {
	w.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	type check struct {
		Namespace, Group, Resource, Verb, Name string
		Allowed                                bool
	}
	checks := []check{
		{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Verb: "create"},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Verb: "patch"},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Verb: "delete"},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Verb: "bind", Name: w.p.namespace + ".hypershell-namespace-allocation.gateway-worker"},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Verb: "bind", Name: "system:openshift:scc:nonroot-v2"},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterroles", Verb: "escalate"},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterrolebindings", Verb: "create"},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterrolebindings", Verb: "patch"},
		{Group: "rbac.authorization.k8s.io", Resource: "clusterrolebindings", Verb: "delete"},
		{Group: "admissionregistration.k8s.io", Resource: "validatingadmissionpolicies", Verb: "create"},
		{Group: "admissionregistration.k8s.io", Resource: "validatingadmissionpolicies", Verb: "patch"},
		{Group: "admissionregistration.k8s.io", Resource: "validatingadmissionpolicies", Verb: "delete"},
		{Group: "admissionregistration.k8s.io", Resource: "validatingadmissionpolicybindings", Verb: "create"},
		{Group: "admissionregistration.k8s.io", Resource: "validatingadmissionpolicybindings", Verb: "patch"},
		{Group: "admissionregistration.k8s.io", Resource: "validatingadmissionpolicybindings", Verb: "delete"},
		{Namespace: "default", Resource: "serviceaccounts/token", Verb: "create", Name: "hypershell-namespace-allocation"},
		{Namespace: w.p.namespace, Resource: "serviceaccounts/token", Verb: "create", Name: "hypershell-namespace-allocation", Allowed: true},
	}

	for _, ns := range []string{"default", "kube-system"} {
		for _, verb := range []string{"get", "list", "watch", "create", "patch", "delete"} {
			checks = append(checks, check{Namespace: ns, Resource: "secrets", Verb: verb})
		}
		checks = append(checks,
			check{Namespace: ns, Resource: "pods", Verb: "create"},
			check{Namespace: ns, Resource: "pods", Verb: "delete"},
			check{Namespace: ns, Resource: "pods/exec", Verb: "create"},
			check{Namespace: ns, Group: "apps", Resource: "deployments", Verb: "patch"})
	}
	for _, verb := range []string{"create", "patch", "delete"} {
		checks = append(checks, check{Resource: "namespaces", Verb: verb})
	}
	checks = append(checks,
		check{Group: "authentication.k8s.io", Resource: "tokenreviews", Verb: "create"},
		check{Resource: "nodes", Verb: "list"},
		check{Namespace: gateway, Group: "agents.x-k8s.io", Resource: "sandboxes", Verb: "create"},
		check{Namespace: gateway, Resource: "secrets", Verb: "get", Name: "openshell-gateway-db-credentials", Allowed: true},
		check{Namespace: gateway, Resource: "secrets", Verb: "get", Name: "unrelated"},
		check{Namespace: gateway, Resource: "secrets", Verb: "list"},
		check{Namespace: gateway, Resource: "secrets", Verb: "patch", Name: "openshell-gateway-db-credentials"},
		check{Namespace: gateway, Resource: "pods", Verb: "delete", Allowed: true},
		check{Namespace: gateway, Resource: "pods/exec", Verb: "create"},
		check{Namespace: gateway, Group: "apps", Resource: "deployments", Verb: "patch", Name: "openshell-gateway"},
		check{Namespace: state, Resource: "secrets", Verb: "get", Name: "openshell-gateway-state", Allowed: true},
		check{Namespace: state, Resource: "secrets", Verb: "get", Name: "unrelated"},
		check{Namespace: state, Resource: "secrets", Verb: "list"},
		check{Namespace: state, Resource: "secrets", Verb: "patch", Name: "openshell-gateway-state"},
		check{Namespace: state, Resource: "pods", Verb: "get"},
		check{Namespace: state, Resource: "pods", Verb: "create"},
		check{Namespace: state, Resource: "pods", Verb: "delete"})

	checks = append(checks,
		check{Namespace: gateway, Group: "cert-manager.io", Resource: "certificates", Verb: "get", Name: publicCertificateName, Allowed: true},
		check{Namespace: gateway, Group: "cert-manager.io", Resource: "certificates/status", Verb: "update", Name: publicCertificateName, Allowed: true},
		check{Namespace: gateway, Group: "cert-manager.io", Resource: "certificates", Verb: "update", Name: publicCertificateName},
		check{Namespace: gateway, Group: "cert-manager.io", Resource: "certificates/status", Verb: "update", Name: "openshell-server-tls"},
		check{Namespace: state, Group: "cert-manager.io", Resource: "certificates/status", Verb: "update", Name: publicCertificateName},
		check{Namespace: "default", Group: "cert-manager.io", Resource: "certificates/status", Verb: "update", Name: publicCertificateName},
		check{Namespace: gateway, Resource: "secrets", Verb: "get", Name: publicCertificateName, Allowed: true},
		check{Namespace: gateway, Resource: "secrets", Verb: "delete", Name: publicCertificateName})

	for _, test := range checks {
		attributes := kube.Object{"namespace": test.Namespace, "group": test.Group, "resource": test.Resource, "verb": test.Verb}
		if resource, subresource, found := strings.Cut(test.Resource, "/"); found {
			attributes["resource"], attributes["subresource"] = resource, subresource
		}
		if test.Name != "" {
			attributes["name"] = test.Name
		}
		review, code, err := w.kubernetes.Request(ctx, "POST", "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", kube.Object{"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "spec": kube.Object{"resourceAttributes": attributes}})
		allowed, ok := kube.Nested(review, "status", "allowed").(bool)
		if err != nil || code != 201 || !ok || allowed != test.Allowed {
			w.t.Fatal("test actor installation access differs", test, code)
		}
	}
	name := w.p.namespace + ".denied-installation"
	_, code, err := w.kubernetes.Request(ctx, "POST", "/apis/rbac.authorization.k8s.io/v1/clusterroles?dryRun=All&fieldValidation=Strict", kube.Object{"apiVersion": "rbac.authorization.k8s.io/v1", "kind": "ClusterRole", "metadata": kube.Object{"name": name}, "rules": []any{}})
	var denied *kube.APIError
	if code != 403 || !errors.As(err, &denied) || denied.StatusCode != 403 {
		w.t.Fatal("test actor cluster role creation was not denied", code)
	}
	if _, code, err := w.kubernetes.Request(ctx, "GET", "/apis/rbac.authorization.k8s.io/v1/clusterroles/"+name, nil); err != nil || code != 404 {
		w.t.Fatal("denied installation probe left a cluster role", code)
	}

	// Use actual reads as well as access reviews. A 404 is not a denial.
	for _, path := range []string{
		"/api/v1/namespaces/default/secrets/stego-denied-read",
		"/api/v1/namespaces/" + gateway + "/secrets/unrelated",
		"/api/v1/namespaces/" + state + "/secrets/unrelated",
	} {
		_, code, err := w.kubernetes.Request(ctx, "GET", path, nil)
		if code != 403 || !errors.As(err, &denied) || denied.StatusCode != 403 {
			w.t.Fatal("test actor Secret read was not denied", code)
		}
	}

	if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
		data, err := json.MarshalIndent(checks, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(directory, "cluster-installation-access.json"), data, 0600) != nil {
			w.t.Fatal("cannot write installation access evidence")
		}
	}
	w.t.Log("Test actor inspection is limited to declared Gateway resources; foreign Secret and workload access is denied")
}
