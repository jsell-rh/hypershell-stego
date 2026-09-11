package contracts

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"text/template"
)

func TestConsoleDeploymentIsolation(t *testing.T) {
	type resource struct {
		Kind     string
		Metadata struct{ Name, Namespace string }
		Spec     struct {
			Template struct {
				Spec struct {
					ServiceAccountName           string
					AutomountServiceAccountToken bool
					Containers                   []struct {
						Env     []struct{ Name, Value string }
						EnvFrom []struct{ SecretRef struct{ Name string } }
						Ports   []struct{ ContainerPort int }
					}
					Volumes []struct {
						Secret *struct {
							SecretName  string
							DefaultMode int
						}
					}
				}
			}
			Ingress []struct {
				From []struct {
					NamespaceSelector struct{ MatchLabels map[string]string }
					PodSelector       struct{ MatchLabels map[string]string }
				}
				Ports []struct {
					Port     int
					Protocol string
				}
			}
			Egress []struct {
				To []struct {
					NamespaceSelector struct{ MatchLabels map[string]string }
					PodSelector       struct{ MatchLabels map[string]string }
				}
				Ports []struct {
					Port     int
					Protocol string
				}
			}
		}
	}
	load := func(path string) []resource {
		t.Helper()
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		source, err := template.New("manifest").Parse(string(data))
		if err != nil {
			t.Fatal(err)
		}
		var rendered bytes.Buffer
		if err := source.Execute(&rendered, map[string]any{"Namespace": "isolated", "Image": "example.test/console@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "FSGroup": 10001}); err != nil {
			t.Fatal(err)
		}
		var doc struct{ Items []resource }
		if err := json.Unmarshal(rendered.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		return doc.Items
	}
	console := load("../console/out/deploy/render/manifest.json.tmpl")
	expected := map[string]bool{"api": false, "database": false, "identity": false, "collector": false, "deployment": false, "apiIngress": false}
	for _, item := range console {
		if item.Metadata.Name != "hypershell-console" || item.Metadata.Namespace != "isolated" {
			t.Fatal("console resource identity differs")
		}
		switch item.Kind {
		case "Deployment":
			pod := item.Spec.Template.Spec
			if pod.ServiceAccountName != "hypershell-console" || pod.AutomountServiceAccountToken || len(pod.Containers) != 1 {
				t.Fatal("console process isolation differs")
			}
			c := pod.Containers[0]
			if len(c.EnvFrom) != 1 || c.EnvFrom[0].SecretRef.Name != "hypershell-console-runtime" {
				t.Fatal("console runtime Secret differs")
			}
			if len(c.Ports) != 1 || c.Ports[0].ContainerPort != 8443 {
				t.Fatal("console listener differs")
			}
			files := false
			for _, volume := range pod.Volumes {
				if volume.Secret != nil {
					if volume.Secret.SecretName != "hypershell-console-files" || volume.Secret.DefaultMode != 0440 {
						t.Fatal("console file Secret differs")
					}
					files = true
				}
			}
			if !files {
				t.Fatal("console file Secret missing")
			}
			env := map[string]string{}
			for _, e := range c.Env {
				env[e.Name] = e.Value
			}
			if env["STEGO_HTTP_REQUIRE_TLS"] != "1" || env["STEGO_DATABASE_ALLOW_INSECURE_LOOPBACK"] != "0" {
				t.Fatal("console TLS is not required")
			}
			expected["deployment"] = true
		case "NetworkPolicy":
			for _, rule := range item.Spec.Egress {
				for _, peer := range rule.To {
					if peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "isolated" {
						continue
					}
					for _, port := range rule.Ports {
						if port.Protocol != "TCP" {
							continue
						}
						switch {
						case peer.PodSelector.MatchLabels["app.kubernetes.io/name"] == "hypershell" && port.Port == 8443:
							expected["api"] = true
						case peer.PodSelector.MatchLabels["app"] == "stego-fixture" && port.Port == 5432:
							expected["database"] = true
						case peer.PodSelector.MatchLabels["app"] == "identity-fixture" && port.Port == 8443:
							expected["identity"] = true
						case peer.PodSelector.MatchLabels["app"] == "stego-fixture" && port.Port == 19093:
							expected["collector"] = true
						}
					}
				}
			}
		}
	}
	for _, item := range load("../out/deploy/render/manifest.json.tmpl") {
		if item.Kind != "NetworkPolicy" {
			continue
		}
		for _, rule := range item.Spec.Ingress {
			for _, peer := range rule.From {
				if peer.NamespaceSelector.MatchLabels["kubernetes.io/metadata.name"] != "isolated" || peer.PodSelector.MatchLabels["app.kubernetes.io/name"] != "hypershell-console" {
					continue
				}
				if len(rule.Ports) != 1 || rule.Ports[0].Port != 8443 || rule.Ports[0].Protocol != "TCP" {
					t.Fatal("console API ingress differs")
				}
				expected["apiIngress"] = true
			}
		}
	}
	for requirement, present := range expected {
		if !present {
			t.Errorf("missing console deployment requirement: %s", requirement)
		}
	}
}
