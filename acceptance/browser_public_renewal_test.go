package acceptance

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strconv"
	"testing"
	"time"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

const publicCertificateName = "openshell-public-tls"

func publicGatewayOwner(id string) kube.Owner {
	return kube.Owner{"hypershell.redhat.io/gateway-id": id, "app.kubernetes.io/managed-by": "hypershell-gateway-controller"}
}

func certificateNumber(value any) int64 {
	number, ok := value.(json.Number)
	if !ok {
		return 0
	}
	result, err := number.Int64()
	if err != nil || result < 1 {
		return 0
	}
	return result
}

// This is the status operation used by cmctl renew. Keep the observed UID and
// revision. A conflict must fail this test; do not repeat a renewal on new state.
func publicRenewalRequest(current kube.Object, id, namespace, host, issuer string, now time.Time) (kube.Object, error) {
	invalid := errors.New("public renewal requires an owned, ready Certificate")
	generation := certificateNumber(kube.Nested(current, "metadata", "generation"))
	if !publicGatewayOwner(id).Matches(current) || kube.String(current, "apiVersion") != "cert-manager.io/v1" || kube.String(current, "kind") != "Certificate" ||
		kube.String(current, "metadata", "name") != publicCertificateName || kube.String(current, "metadata", "namespace") != namespace ||
		kube.String(current, "metadata", "uid") == "" || kube.String(current, "metadata", "resourceVersion") == "" || kube.String(current, "metadata", "deletionTimestamp") != "" || generation == 0 ||
		kube.String(current, "spec", "secretName") != publicCertificateName || kube.String(current, "spec", "issuerRef", "kind") != "ClusterIssuer" || kube.String(current, "spec", "issuerRef", "name") != issuer ||
		kube.String(current, "spec", "privateKey", "rotationPolicy") != "Always" || certificateNumber(kube.Nested(current, "status", "revision")) == 0 {
		return nil, invalid
	}
	names, ok := kube.Nested(current, "spec", "dnsNames").([]any)
	if !ok || len(names) != 1 || names[0] != host {
		return nil, invalid
	}
	raw, err := json.Marshal(current)
	if err != nil {
		return nil, invalid
	}
	var copy kube.Object
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&copy) != nil {
		return nil, invalid
	}
	conditions, ok := kube.Nested(copy, "status", "conditions").([]any)
	if !ok || len(conditions) == 0 || len(conditions) > 32 {
		return nil, invalid
	}
	ready := false
	issuing := -1
	seen := map[string]bool{}
	for i, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if !ok {
			return nil, invalid
		}
		kind, ok := condition["type"].(string)
		if !ok || kind == "" || seen[kind] {
			return nil, invalid
		}
		seen[kind] = true
		switch kind {
		case "Ready":
			ready = condition["status"] == "True" && certificateNumber(condition["observedGeneration"]) == generation
		case "Issuing":
			if condition["status"] != "False" {
				return nil, invalid
			}
			issuing = i
		}
	}
	if !ready || now.IsZero() {
		return nil, invalid
	}
	condition := map[string]any{"type": "Issuing", "status": "True", "reason": "ManuallyTriggered", "message": "Certificate re-issuance manually triggered", "observedGeneration": json.Number(strconv.FormatInt(generation, 10)), "lastTransitionTime": now.UTC().Format(time.RFC3339)}
	if issuing < 0 {
		conditions = append(conditions, condition)
	} else {
		conditions[issuing] = condition
	}
	copy["status"].(map[string]any)["conditions"] = conditions
	return copy, nil
}

func renewalFixture() kube.Object {
	return kube.Object{"apiVersion": "cert-manager.io/v1", "kind": "Certificate", "metadata": kube.Object{"name": publicCertificateName, "namespace": "gateway-a", "uid": "cert-uid", "resourceVersion": "77", "generation": json.Number("2"), "labels": kube.Object{"hypershell.redhat.io/gateway-id": "gateway-id", "app.kubernetes.io/managed-by": "hypershell-gateway-controller"}},
		"spec":   kube.Object{"secretName": publicCertificateName, "dnsNames": []any{"gw-gateway-a.example.test"}, "issuerRef": kube.Object{"name": "public-issuer", "kind": "ClusterIssuer"}, "privateKey": kube.Object{"rotationPolicy": "Always"}},
		"status": kube.Object{"revision": json.Number("3"), "renewalTime": "later", "conditions": []any{map[string]any{"type": "Ready", "status": "True", "observedGeneration": json.Number("2")}}}}
}

func TestPublicRenewalPreservesObservedState(t *testing.T) {
	current := renewalFixture()
	before, _ := json.Marshal(current)
	requested, err := publicRenewalRequest(current, "gateway-id", "gateway-a", "gw-gateway-a.example.test", "public-issuer", time.Unix(1000, 0))
	if err != nil {
		t.Fatal(err)
	}
	after, _ := json.Marshal(current)
	if !bytes.Equal(before, after) {
		t.Fatal("renewal changed its observation")
	}
	if kube.String(requested, "metadata", "uid") != "cert-uid" || kube.String(requested, "metadata", "resourceVersion") != "77" {
		t.Fatal("renewal lost write preconditions")
	}
	status := requested["status"].(map[string]any)
	conditions := status["conditions"].([]any)
	if len(conditions) != 2 || conditions[1].(map[string]any)["type"] != "Issuing" || conditions[1].(map[string]any)["status"] != "True" || conditions[1].(map[string]any)["reason"] != "ManuallyTriggered" {
		t.Fatal("renewal condition differs")
	}
	status["conditions"] = conditions[:1]
	restored, _ := json.Marshal(requested)
	if !bytes.Equal(before, restored) {
		t.Fatal("renewal changed unrelated fields")
	}
	current["status"].(kube.Object)["conditions"] = append(kube.Nested(current, "status", "conditions").([]any), map[string]any{"type": "Issuing", "status": "False"})
	requested, err = publicRenewalRequest(current, "gateway-id", "gateway-a", "gw-gateway-a.example.test", "public-issuer", time.Unix(1000, 0))
	if err != nil || len(kube.Nested(requested, "status", "conditions").([]any)) != 2 {
		t.Fatal("renewal duplicated the existing condition", err)
	}
}

func TestPublicRenewalRejectsStaleOrForeignState(t *testing.T) {
	for name, change := range map[string]func(kube.Object){
		"owner": func(c kube.Object) {
			c["metadata"].(kube.Object)["labels"] = kube.Object{"hypershell.redhat.io/gateway-id": "other", "app.kubernetes.io/managed-by": "hypershell-gateway-controller"}
		},
		"namespace":          func(c kube.Object) { c["metadata"].(kube.Object)["namespace"] = "other" },
		"uid":                func(c kube.Object) { delete(c["metadata"].(kube.Object), "uid") },
		"revision":           func(c kube.Object) { delete(c["metadata"].(kube.Object), "resourceVersion") },
		"deleting":           func(c kube.Object) { c["metadata"].(kube.Object)["deletionTimestamp"] = "now" },
		"generation":         func(c kube.Object) { c["metadata"].(kube.Object)["generation"] = json.Number("3") },
		"numeric generation": func(c kube.Object) { c["metadata"].(kube.Object)["generation"] = 2.5 },
		"host":               func(c kube.Object) { c["spec"].(kube.Object)["dnsNames"] = []any{"other.example.test"} },
		"key reuse":          func(c kube.Object) { c["spec"].(kube.Object)["privateKey"] = kube.Object{"rotationPolicy": "Never"} },
		"issuer": func(c kube.Object) {
			c["spec"].(kube.Object)["issuerRef"] = kube.Object{"name": "other", "kind": "ClusterIssuer"}
		},
		"already issuing": func(c kube.Object) {
			c["status"].(kube.Object)["conditions"] = append(kube.Nested(c, "status", "conditions").([]any), map[string]any{"type": "Issuing", "status": "True"})
		},
		"duplicate ready": func(c kube.Object) {
			v := kube.Nested(c, "status", "conditions").([]any)
			c["status"].(kube.Object)["conditions"] = append(v, v[0])
		},
		"not ready": func(c kube.Object) {
			kube.Nested(c, "status", "conditions").([]any)[0].(map[string]any)["status"] = "False"
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := renewalFixture()
			change(c)
			before, _ := json.Marshal(c)
			result, err := publicRenewalRequest(c, "gateway-id", "gateway-a", "gw-gateway-a.example.test", "public-issuer", time.Unix(1000, 0))
			after, _ := json.Marshal(c)
			if err == nil || result != nil || !reflect.DeepEqual(before, after) {
				t.Fatal("unsafe renewal accepted or input changed")
			}
		})
	}
}
