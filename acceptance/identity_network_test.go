package acceptance

import "testing"

func TestIdentityFixturePermitsOnlyAssignedGatewayConsoles(t *testing.T) {
	const marker = "test-allocator"
	// A peer with both selectors requires both to match. A peer with only a
	// Pod selector applies within the identity provider's own namespace.
	matches := func(selector map[string]any, labels map[string]string) bool {
		for key, value := range selector["matchLabels"].(map[string]any) {
			if labels[key] != value {
				return false
			}
		}
		return true
	}
	allowed := func(namespace, profile, allocator, app string) bool {
		podLabels := map[string]string{"app.kubernetes.io/name": app}
		namespaceLabels := map[string]string{"stego.dev/allocator": allocator, "stego.dev/allocation-profile": profile}
		for _, raw := range identityFixtureIngressPeers(marker) {
			peer := raw.(map[string]any)
			if ns, ok := peer["namespaceSelector"].(map[string]any); ok {
				if !matches(ns, namespaceLabels) {
					continue
				}
			} else if namespace != "control" {
				continue
			}
			selector := peer["podSelector"].(map[string]any)
			if _, ok := selector["matchLabels"]; ok {
				if matches(selector, podLabels) {
					return true
				}
				continue
			}
			accepted := true
			for _, raw := range selector["matchExpressions"].([]any) {
				expression := raw.(map[string]any)
				if expression["operator"] != "Exists" {
					t.Fatal("unsupported test selector")
				}
				if _, present := podLabels[expression["key"].(string)]; !present {
					accepted = false
				}
			}
			if accepted {
				return true
			}
		}
		return false
	}
	for _, test := range []struct {
		name, namespace, profile, allocator, app string
		want                                     bool
	}{
		{"assigned console", "gateway-one", "gateway", marker, "hypershell-gateway-console", true},
		{"another assigned console", "gateway-two", "gateway", marker, "hypershell-gateway-console", true},
		{"foreign allocator", "gateway-one", "gateway", "foreign", "hypershell-gateway-console", false},
		{"state namespace", "state", "gateway-state", marker, "hypershell-gateway-console", false},
		{"unrelated pod", "gateway-one", "gateway", marker, "unrelated", false},
		{"unassigned namespace", "foreign", "", "", "hypershell-gateway-console", false},
		{"management console", "control", "", "", "hypershell-console", true},
		{"foreign management console", "foreign", "", "", "hypershell-console", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := allowed(test.namespace, test.profile, test.allocator, test.app); got != test.want {
				t.Fatalf("identity ingress allowed=%v, want %v", got, test.want)
			}
		})
	}
}
