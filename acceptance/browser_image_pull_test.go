package acceptance

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// Use a separate image pull identity. The test driver's token must never enter
// a Gateway namespace or its image pull Secret.
func (w *browserGatewayWorkload) imagePullConfig(image string) []byte {
	w.t.Helper()
	const registry = "image-registry.openshift-image-registry.svc:5000"
	const namespace, account = "stego-ci-access", "gateway-console-pull"
	if !strings.HasPrefix(image, registry+"/stego-service-ci/hypershell-gateway-console@sha256:") || w.p.namespace != "stego-service-ci" {
		w.t.Fatal("image pull fixture target differs")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	started := time.Now()
	response, code, err := w.kubernetes.Request(ctx, http.MethodPost, "/api/v1/namespaces/"+namespace+"/serviceaccounts/"+account+"/token", kube.Object{
		"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest", "spec": kube.Object{"expirationSeconds": 1800},
	})
	if err != nil || code != http.StatusCreated {
		w.t.Fatal("image pull credential request failed", code)
	}
	token := kube.String(response, "status", "token")
	expires, err := time.Parse(time.RFC3339, kube.String(response, "status", "expirationTimestamp"))
	if err != nil || len(token) == 0 || len(token) > 8192 || expires.Before(started.Add(20*time.Minute)) || expires.After(time.Now().Add(1805*time.Second)) {
		w.t.Fatal("image pull credential lifetime or size differs")
	}
	file := filepath.Join(w.t.TempDir(), "image-pull-token")
	if err := os.WriteFile(file, []byte(token), 0600); err != nil {
		w.t.Fatal("cannot store the private image pull credential")
	}
	defer os.Remove(file)
	client, err := kube.New(kube.Options{ServerURL: w.options.ServerURL, CAFile: w.options.CAFile, TokenFile: file})
	if err != nil {
		w.t.Fatal("image pull test client setup failed")
	}
	defer client.Close()
	identity, code, err := client.Request(ctx, http.MethodPost, "/apis/authentication.k8s.io/v1/selfsubjectreviews", kube.Object{"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview"})
	if err != nil || code != http.StatusCreated || kube.String(identity, "status", "userInfo", "username") != "system:serviceaccount:"+namespace+":"+account {
		w.t.Fatal("image pull credential has a different authenticated identity")
	}
	type check struct {
		Verb, Group, Resource, Subresource, Namespace, Name string
		Allowed                                             bool
	}
	checks := []check{
		{"get", "image.openshift.io", "imagestreams", "layers", w.p.namespace, "hypershell-gateway-console", true},
		{"get", "image.openshift.io", "imagestreams", "layers", w.p.namespace, "hypershell", false},
		{"update", "image.openshift.io", "imagestreams", "layers", w.p.namespace, "hypershell-gateway-console", false},
		{"get", "", "secrets", "", w.p.namespace, "hypershell-files", false},
		{"create", "", "pods", "", w.p.namespace, "", false},
		{"create", "rbac.authorization.k8s.io", "rolebindings", "", w.p.namespace, "", false},
		{"create", "", "serviceaccounts", "token", namespace, account, false},
	}
	for _, test := range checks {
		review, code, err := client.Request(ctx, http.MethodPost, "/apis/authorization.k8s.io/v1/selfsubjectaccessreviews", kube.Object{
			"apiVersion": "authorization.k8s.io/v1", "kind": "SelfSubjectAccessReview", "spec": kube.Object{"resourceAttributes": kube.Object{
				"verb": test.Verb, "group": test.Group, "resource": test.Resource, "subresource": test.Subresource, "namespace": test.Namespace, "name": test.Name,
			}},
		})
		allowed, ok := kube.Nested(review, "status", "allowed").(bool)
		if err != nil || code != http.StatusCreated || !ok || allowed != test.Allowed || kube.String(review, "status", "evaluationError") != "" {
			w.t.Fatal("image pull identity permission differs", test, code)
		}
	}
	config, err := json.Marshal(map[string]any{"auths": map[string]any{registry: map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte("serviceaccount:" + token))}}})
	if err != nil || kube.ValidateImagePullConfig(config, []string{registry}) != nil {
		w.t.Fatal("image pull fixture configuration is invalid")
	}
	if dir := os.Getenv("STEGO_BROWSER_ARTIFACT_DIR"); dir != "" {
		record, err := json.MarshalIndent(struct {
			Expires time.Time
			Checks  []check
		}{expires, checks}, "", "  ")
		if err != nil || os.WriteFile(filepath.Join(dir, "image-pull-permissions.json"), record, 0600) != nil {
			w.t.Fatal("cannot write image pull permission evidence")
		}
	}
	return config
}
