package acceptance

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"testing"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

const publicWorkerPolicy = "hypershell-gateway-workload"

func publicPolicySpec(current kube.Object, namespace string) (map[string]json.RawMessage, error) {
	invalid := errors.New("public fault requires the assigned worker network policy")
	if kube.String(current, "apiVersion") != "networking.k8s.io/v1" || kube.String(current, "kind") != "NetworkPolicy" || kube.String(current, "metadata", "name") != publicWorkerPolicy || kube.String(current, "metadata", "namespace") != namespace || kube.String(current, "metadata", "uid") == "" || kube.String(current, "metadata", "resourceVersion") == "" || kube.String(current, "metadata", "deletionTimestamp") != "" {
		return nil, invalid
	}
	raw, err := json.Marshal(current["spec"])
	if err != nil {
		return nil, invalid
	}
	var spec map[string]json.RawMessage
	if json.Unmarshal(raw, &spec) != nil || spec == nil {
		return nil, invalid
	}
	expected, _ := json.Marshal(kube.Object{"matchLabels": kube.Object{"app.kubernetes.io/name": publicWorkerPolicy}})
	if !bytes.Equal(spec["podSelector"], expected) {
		return nil, invalid
	}
	var types []string
	if json.Unmarshal(spec["policyTypes"], &types) != nil || len(types) != 2 || !((types[0] == "Ingress" && types[1] == "Egress") || (types[0] == "Egress" && types[1] == "Ingress")) {
		return nil, invalid
	}
	return spec, nil
}

func publicPolicyRuleCounts(spec map[string]json.RawMessage) (map[string]int, error) {
	invalid := errors.New("public fault requires explicit egress rules")
	raw, ok := spec["egress"]
	if !ok {
		return nil, invalid
	}
	var rules []json.RawMessage
	if json.Unmarshal(raw, &rules) != nil || rules == nil {
		return nil, invalid
	}
	counts := map[string]int{}
	for _, rule := range rules {
		counts[string(rule)]++
	}
	return counts, nil
}

func verifyPublicEgressRemoval(before, after kube.Object, namespace string, endpoints []string) error {
	invalid := errors.New("public fault changed more than the selected egress rules")
	if kube.String(before, "metadata", "uid") != kube.String(after, "metadata", "uid") || kube.String(before, "metadata", "resourceVersion") == kube.String(after, "metadata", "resourceVersion") {
		return invalid
	}
	original, err := publicPolicySpec(before, namespace)
	if err != nil {
		return err
	}
	blocked, err := publicPolicySpec(after, namespace)
	if err != nil {
		return err
	}
	expected, err := publicPolicyRuleCounts(original)
	if err != nil {
		return err
	}
	observed, err := publicPolicyRuleCounts(blocked)
	if err != nil {
		return err
	}
	if len(endpoints) == 0 || len(endpoints) > 16 {
		return invalid
	}
	seen := map[netip.AddrPort]bool{}
	for _, endpoint := range endpoints {
		pair, err := netip.ParseAddrPort(endpoint)
		if err != nil || pair.Port() != 443 || !pair.Addr().IsGlobalUnicast() || pair.Addr().IsLoopback() || pair.Addr().Is4In6() || pair.Addr().Zone() != "" || seen[pair] {
			return invalid
		}
		seen[pair] = true
		rule := kube.Object{"to": []any{kube.Object{"ipBlock": kube.Object{"cidr": netip.PrefixFrom(pair.Addr(), pair.Addr().BitLen()).String()}}}, "ports": []any{kube.Object{"protocol": "TCP", "port": 443}}}
		raw, _ := json.Marshal(rule)
		key := string(raw)
		if expected[key] == 0 {
			return invalid
		}
		expected[key]--
		if expected[key] == 0 {
			delete(expected, key)
		}
	}
	delete(original, "egress")
	delete(blocked, "egress")
	if !reflect.DeepEqual(original, blocked) || !reflect.DeepEqual(expected, observed) {
		return invalid
	}
	return nil
}

func policyFixture(version string, public bool) kube.Object {
	rules := []any{kube.Object{"to": []any{kube.Object{"ipBlock": kube.Object{"cidr": "192.0.2.1/32"}}}, "ports": []any{kube.Object{"port": 443, "protocol": "TCP"}}}}
	if public {
		rules = append(rules, kube.Object{"to": []any{kube.Object{"ipBlock": kube.Object{"cidr": "192.0.2.3/32"}}}, "ports": []any{kube.Object{"port": 443, "protocol": "TCP"}}})
	}
	return kube.Object{"apiVersion": "networking.k8s.io/v1", "kind": "NetworkPolicy", "metadata": kube.Object{"name": publicWorkerPolicy, "namespace": "control", "uid": "policy-id", "resourceVersion": version}, "spec": kube.Object{"podSelector": kube.Object{"matchLabels": kube.Object{"app.kubernetes.io/name": publicWorkerPolicy}}, "policyTypes": []string{"Ingress", "Egress"}, "ingress": []any{}, "egress": rules}}
}

func TestPublicFaultVerifiesLivePolicyDifference(t *testing.T) {
	before, after := policyFixture("10", true), policyFixture("11", false)
	original, _ := json.Marshal(before)
	blocked, _ := json.Marshal(after)
	if err := verifyPublicEgressRemoval(before, after, "control", []string{"192.0.2.3:443"}); err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(before)
	a, _ := json.Marshal(after)
	if !bytes.Equal(b, original) || !bytes.Equal(a, blocked) {
		t.Fatal("policy check changed observations")
	}
	ipv6 := kube.Object{"to": []any{kube.Object{"ipBlock": kube.Object{"cidr": "2001:db8::3/128"}}}, "ports": []any{kube.Object{"protocol": "TCP", "port": 443}}}
	before["spec"].(kube.Object)["egress"] = append(before["spec"].(kube.Object)["egress"].([]any), ipv6)
	if err := verifyPublicEgressRemoval(before, after, "control", []string{"[2001:db8::3]:443", "192.0.2.3:443"}); err != nil {
		t.Fatal(err)
	}
	before = policyFixture("10", true)
	// A required destination can share the same IP and port. Remove one rule,
	// not all rules for that pair. The live denial stage must still prove denial.
	rule := before["spec"].(kube.Object)["egress"].([]any)[1]
	before["spec"].(kube.Object)["egress"] = append(before["spec"].(kube.Object)["egress"].([]any), rule)
	after["spec"].(kube.Object)["egress"] = append(after["spec"].(kube.Object)["egress"].([]any), rule)
	if err := verifyPublicEgressRemoval(before, after, "control", []string{"192.0.2.3:443"}); err != nil {
		t.Fatal(err)
	}
}

func TestPublicFaultRejectsUnrelatedPolicyChanges(t *testing.T) {
	for name, change := range map[string]func(kube.Object){
		"no change":            func(c kube.Object) { c["spec"] = policyFixture("10", true)["spec"] },
		"wrong UID":            func(c kube.Object) { c["metadata"].(kube.Object)["uid"] = "other" },
		"same revision":        func(c kube.Object) { c["metadata"].(kube.Object)["resourceVersion"] = "10" },
		"namespace":            func(c kube.Object) { c["metadata"].(kube.Object)["namespace"] = "other" },
		"selector":             func(c kube.Object) { c["spec"].(kube.Object)["podSelector"] = kube.Object{} },
		"policy type":          func(c kube.Object) { c["spec"].(kube.Object)["policyTypes"] = []string{"Ingress"} },
		"ingress":              func(c kube.Object) { c["spec"].(kube.Object)["ingress"] = []any{kube.Object{}} },
		"required destination": func(c kube.Object) { c["spec"].(kube.Object)["egress"] = []any{} },
		"broad rule": func(c kube.Object) {
			c["spec"].(kube.Object)["egress"] = append(c["spec"].(kube.Object)["egress"].([]any), kube.Object{})
		},
	} {
		t.Run(name, func(t *testing.T) {
			before, after := policyFixture("10", true), policyFixture("11", false)
			change(after)
			if verifyPublicEgressRemoval(before, after, "control", []string{"192.0.2.3:443"}) == nil {
				t.Fatal("unrelated policy change accepted")
			}
		})
	}
	for _, endpoints := range [][]string{nil, {"192.0.2.3:80"}, {"192.0.2.4:443"}, {"192.0.2.3:443", "192.0.2.3:443"}} {
		if verifyPublicEgressRemoval(policyFixture("10", true), policyFixture("11", false), "control", endpoints) == nil {
			t.Fatal("invalid fault binding accepted")
		}
	}
}
