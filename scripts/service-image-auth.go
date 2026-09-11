//go:build ignore

// This command writes private registry credentials inside the test Job.
package main

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"strings"
)

func main() {
	token, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/token")
	if err != nil {
		panic("cannot read test service account")
	}
	value := map[string]any{"auths": map[string]any{"image-registry.openshift-image-registry.svc:5000": map[string]string{"auth": base64.StdEncoding.EncodeToString([]byte("serviceaccount:" + strings.TrimSpace(string(token))))}}}
	data, err := json.Marshal(value)
	if err != nil {
		panic("cannot encode registry credentials")
	}
	if err := os.WriteFile("/work/registry-auth.json", data, 0600); err != nil {
		panic("cannot store registry credentials")
	}
}
