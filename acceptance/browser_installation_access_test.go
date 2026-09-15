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
func (w *browserGatewayWorkload) checkInstallationAccess() {
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
	if directory := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); directory != "" {
		data, err := json.MarshalIndent(checks, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(directory, "cluster-installation-access.json"), data, 0600) != nil {
			w.t.Fatal("cannot write installation access evidence")
		}
	}
	w.t.Log("Test actor cannot change cluster roles or admission policies; its token requests stay in the control namespace")
}
