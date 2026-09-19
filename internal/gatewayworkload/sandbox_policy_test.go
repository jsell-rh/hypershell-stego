package gatewayworkload

import (
	"encoding/json"
	"os"
	"testing"

	"cel.dev/cel-go/cel"
	"gopkg.in/yaml.v3"
)

// Check the application rule and its generated policy. These checks do not
// require a Kubernetes cluster or start a Sandbox.
func TestSandboxSecretEnvironmentPolicy(t *testing.T) {
	const message = "Sandbox containers cannot read Secrets through environment variables"
	type rule struct {
		Expression string `yaml:"expression" json:"expression"`
		Message    string `yaml:"message" json:"message"`
	}
	var service struct {
		Overrides map[string]struct {
			Profiles []struct {
				Name  string `yaml:"name"`
				Rules []rule `yaml:"pod_validations"`
			} `yaml:"allocation_profiles"`
		} `yaml:"overrides"`
	}
	data, err := os.ReadFile("../../service.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &service); err != nil {
		t.Fatal(err)
	}
	var expression string
	for _, profile := range service.Overrides["kubernetes-service"].Profiles {
		if profile.Name == "sandbox" {
			for _, r := range profile.Rules {
				if r.Message == message {
					expression = r.Expression
				}
			}
		}
	}
	if expression == "" {
		t.Fatal("Sandbox Secret environment rule is absent")
	}
	var manifest struct {
		Items []struct {
			Kind string
			Spec struct {
				FailurePolicy string
				Validations   []rule
			}
		}
	}
	data, err = os.ReadFile("../../out/deploy/render/worker-namespace-allocation.json.tmpl")
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &manifest); err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, item := range manifest.Items {
		for _, r := range item.Spec.Validations {
			if r.Message == message {
				if item.Kind != "ValidatingAdmissionPolicy" || item.Spec.FailurePolicy != "Fail" || r.Expression != expression {
					t.Fatal("Generated policy changed the Sandbox Secret environment rule")
				}
				found++
			}
		}
	}
	if found != 1 {
		t.Fatal("Expected one generated Sandbox Secret environment rule", found)
	}
	env, err := cel.NewEnv(cel.Variable("variables", cel.MapType(cel.StringType, cel.DynType)))
	if err != nil {
		t.Fatal(err)
	}
	ast, issues := env.Compile(expression)
	if issues.Err() != nil {
		t.Fatal(issues.Err())
	}
	program, err := env.Program(ast, cel.CostLimit(10000))
	if err != nil {
		t.Fatal(err)
	}
	for _, container := range []string{"agent", "workspace-init", "openshell-network-init", "openshell-supervisor-network"} {
		for _, test := range []struct {
			name    string
			fields  string
			allowed bool
		}{
			{"absent", `{}`, true},
			{"empty", `{"env":[],"envFrom":[]}`, true},
			{"literal", `{"env":[{"name":"TASK","value":"example"}]}`, true},
			{"field", `{"env":[{"name":"POD_NAME","valueFrom":{"fieldRef":{"fieldPath":"metadata.name"}}}]}`, true},
			{"config key", `{"env":[{"name":"TASK","valueFrom":{"configMapKeyRef":{"name":"task","key":"name"}}}]}`, true},
			{"config map", `{"envFrom":[{"configMapRef":{"name":"task"}}]}`, true},
			{"secret key", `{"env":[{"name":"KEY","valueFrom":{"secretKeyRef":{"name":"openshell-client-tls","key":"tls.key"}}}]}`, false},
			{"secret map", `{"envFrom":[{"secretRef":{"name":"openshell-client-tls"}}]}`, false},
			{"optional secret", `{"envFrom":[{"secretRef":{"name":"openshell-client-tls","optional":true}}]}`, false},
			{"mixed sources", `{"envFrom":[{"configMapRef":{"name":"task"}},{"prefix":"TASK_","secretRef":{"name":"openshell-client-tls"}}]}`, false},
		} {
			t.Run(container+"/"+test.name, func(t *testing.T) {
				var c map[string]any
				if err := json.Unmarshal([]byte(test.fields), &c); err != nil {
					t.Fatal(err)
				}
				c["name"] = container
				if sandboxEnvironmentAllowed(c) != test.allowed {
					t.Fatal("Setup probe accepted a different Secret environment rule")
				}
				result, _, err := program.Eval(map[string]any{"variables": map[string]any{"containers": []any{map[string]any{"name": "unchanged"}, c}}})
				if err != nil || result.Value() != test.allowed {
					t.Fatal("Unexpected Secret environment decision", result, err)
				}
			})
		}
	}
}
