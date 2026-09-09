package gatewayworkload

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

const admissionAPI = "/apis/admissionregistration.k8s.io/v1"
const mutationAPI = "/apis/admissionregistration.k8s.io/v1beta1"

// The operator selects and installs the isolated runtime. Admission prevents
// an application request from selecting a different runtime or host access.
func sandboxAdmission(id, ns, runtimeClass, supervisor string) []resource {
	policy := definition("admissionregistration.k8s.io/v1", "ValidatingAdmissionPolicy", ns, id)
	expressions := []string{
		fmt.Sprintf("has(object.spec.runtimeClassName) && object.spec.runtimeClassName == %q", runtimeClass),
		"(!has(object.spec.hostNetwork) || !object.spec.hostNetwork) && (!has(object.spec.hostPID) || !object.spec.hostPID) && (!has(object.spec.hostIPC) || !object.spec.hostIPC)",
		"object.spec.serviceAccountName == 'openshell-gateway-sandbox' && has(object.spec.automountServiceAccountToken) && !object.spec.automountServiceAccountToken",
		"!has(object.metadata.annotations) || object.metadata.annotations.all(k, k in ['openshell.io/sandbox-id', 'agents.x-k8s.io/propagated-labels', 'agents.x-k8s.io/propagated-annotations'])",
		"!has(object.spec.resourceClaims) || size(object.spec.resourceClaims) == 0",
		"!has(object.spec.ephemeralContainers) || size(object.spec.ephemeralContainers) == 0",
		"!has(object.spec.securityContext) || !has(object.spec.securityContext.sysctls) || size(object.spec.securityContext.sysctls) == 0",
		"variables.containers.all(c, !has(c.securityContext) || !has(c.securityContext.privileged) || !c.securityContext.privileged)",
		"variables.containers.all(c, !has(c.ports) || c.ports.all(p, !has(p.hostPort) || p.hostPort == 0))",
		"variables.containers.all(c, !has(c.volumeMounts) || c.volumeMounts.all(m, !has(m.mountPropagation) || m.mountPropagation == 'None'))",
		fmt.Sprintf("variables.containers.all(c, !has(c.securityContext) || !has(c.securityContext.capabilities) || !has(c.securityContext.capabilities.add) || c.securityContext.capabilities.add.all(a, c.image == %q && ((c.name == 'openshell-network-init' && a in ['NET_ADMIN','NET_RAW','CHOWN','FOWNER']) || (c.name == 'openshell-supervisor-network' && a in ['SYS_PTRACE','DAC_READ_SEARCH']))))", supervisor),
		fmt.Sprintf("variables.containers.all(c, !(c.name in ['openshell-network-init','openshell-supervisor-network']) || c.image == %q)", supervisor),
		"variables.containers.all(c, c.name in ['agent','workspace-init','openshell-network-init','openshell-supervisor-network'])",
		"variables.containers.all(c, !(c.name in ['agent','workspace-init']) || (has(c.securityContext) && has(c.securityContext.runAsNonRoot) && c.securityContext.runAsNonRoot && has(c.securityContext.runAsUser) && c.securityContext.runAsUser != 0 && has(c.securityContext.allowPrivilegeEscalation) && !c.securityContext.allowPrivilegeEscalation && has(c.securityContext.capabilities) && has(c.securityContext.capabilities.drop) && 'ALL' in c.securityContext.capabilities.drop && (!has(c.volumeMounts) || c.volumeMounts.all(m, !(m.name in ['openshell-client-tls','openshell-sa-token'])))))",
		"variables.containers.all(c, !has(c.volumeDevices) || size(c.volumeDevices) == 0)",
		"variables.containers.all(c, !has(c.resources) || !has(c.resources.claims) || size(c.resources.claims) == 0)",
		"variables.containers.all(c, !has(c.resources) || ((!has(c.resources.limits) || c.resources.limits.all(k, k in ['cpu','memory','ephemeral-storage'])) && (!has(c.resources.requests) || c.resources.requests.all(k, k in ['cpu','memory','ephemeral-storage']))))",
		fmt.Sprintf("!has(object.spec.volumes) || object.spec.volumes.all(v, has(v.emptyDir) || (has(v.image) && v.image.reference == %q) || (has(v.secret) && v.name == 'openshell-client-tls' && v.secret.secretName == 'openshell-client-tls') || (has(v.projected) && v.name == 'openshell-sa-token' && v.projected.sources.all(s, has(s.serviceAccountToken) && has(s.serviceAccountToken.audience) && s.serviceAccountToken.audience == 'openshell-gateway' && has(s.serviceAccountToken.expirationSeconds) && s.serviceAccountToken.expirationSeconds <= 3600)) || (has(v.persistentVolumeClaim) && v.name == 'workspace' && has(object.metadata.ownerReferences) && object.metadata.ownerReferences.exists(o, o.kind == 'Sandbox' && o.apiVersion.startsWith('agents.x-k8s.io/') && has(o.controller) && o.controller && v.persistentVolumeClaim.claimName == 'workspace-' + o.name)))", supervisor),
	}
	validations := []object{}
	for i, expr := range expressions {
		validations = append(validations, object{"expression": expr, "reason": "Forbidden", "message": fmt.Sprintf("Sandbox isolation rule %d denied this Pod", i+1)})
	}
	policy["spec"] = object{
		"failurePolicy":    "Fail",
		"matchConstraints": object{"resourceRules": []object{{"apiGroups": []string{""}, "apiVersions": []string{"v1"}, "operations": []string{"CREATE", "UPDATE"}, "resources": []string{"pods", "pods/ephemeralcontainers"}}}},
		"matchConditions":  []object{{"name": "sandbox-namespace", "expression": fmt.Sprintf("request.namespace == %q", ns)}},
		"variables":        []object{{"name": "containers", "expression": "object.spec.containers + (has(object.spec.initContainers) ? object.spec.initContainers : [])"}},
		"validations":      validations,
	}
	binding := definition("admissionregistration.k8s.io/v1", "ValidatingAdmissionPolicyBinding", ns, id)
	binding["spec"] = object{"policyName": ns, "validationActions": []string{"Deny"}}
	return []resource{{admissionAPI + "/validatingadmissionpolicies", policy}, {admissionAPI + "/validatingadmissionpolicybindings", binding}}
}

func (k *Kubernetes) ensureSandbox(ctx context.Context, id, ns, gatewayCore string, roots *x509.CertPool) error {
	runtimeClass := k.options.SandboxRuntimeClass
	if _, code, err := k.client.Request(ctx, http.MethodGet, "/apis/node.k8s.io/v1/runtimeclasses/"+runtimeClass, nil); err != nil {
		return err
	} else if code == 404 {
		return errors.New("Sandbox runtime class is not installed")
	}
	namespace := definition("v1", "Namespace", ns, id)
	// Create with restricted policy. Do not change an existing namespace until
	// both dry-run admission checks prove the selected policy is active.
	existing, code, err := k.client.Request(ctx, http.MethodGet, "/api/v1/namespaces/"+ns, nil)
	if err != nil {
		return err
	}
	if code == 404 {
		if _, err := k.ensure(ctx, "/api/v1/namespaces", sandboxRestrictedNamespace(namespace), id); err != nil {
			return err
		}
	} else if !owner(id).Matches(existing) {
		return errors.New("Sandbox namespace has a different owner")
	}
	core := "/api/v1/namespaces/" + ns
	if _, err := k.ensure(ctx, core+"/serviceaccounts", definition("v1", "ServiceAccount", Name+"-sandbox", id), id); err != nil {
		return err
	}
	entries := append(sandboxMutation(id, ns), sandboxAdmission(id, ns, runtimeClass, k.options.SupervisorImage)...)
	for _, entry := range entries {
		if _, err := k.ensure(ctx, entry.path, entry.object, id); err != nil {
			return err
		}
	}
	probe := definition("v1", "Pod", "isolation-check", id)
	probe["spec"] = object{"runtimeClassName": runtimeClass, "serviceAccountName": Name + "-sandbox", "automountServiceAccountToken": false,
		"securityContext": object{"runAsNonRoot": true, "runAsUser": 1000, "seccompProfile": object{"type": "RuntimeDefault"}},
		"initContainers":  []object{{"name": "workspace-init", "image": k.options.SandboxImage, "securityContext": object{"runAsUser": 0}}},
		"volumes":         []object{{"name": "openshell-sidecar-state", "emptyDir": object{}}},
		"containers":      []object{{"name": "agent", "image": k.options.SandboxImage, "securityContext": object{"runAsNonRoot": true, "runAsUser": 1000, "allowPrivilegeEscalation": false, "capabilities": object{"drop": []string{"ALL"}}}}}}
	if result, _, err := k.client.Request(ctx, http.MethodPost, core+"/pods?dryRun=All", probe); err != nil {
		return err
	} else if !sandboxSocketInMemory(result) {
		return ErrPending
	}
	delete(probe["spec"].(object), "runtimeClassName")
	if _, code, _ := k.client.Request(ctx, http.MethodPost, core+"/pods?dryRun=All", probe); code != http.StatusForbidden {
		return ErrPending
	}
	namespace["metadata"].(object)["labels"].(object)["pod-security.kubernetes.io/enforce"] = "privileged"
	if _, err := k.ensure(ctx, "/api/v1/namespaces", namespace, id); err != nil {
		return err
	}

	// Copy only the client identity. Database and signing keys stay outside this namespace.
	secret, code, err := k.client.Request(ctx, http.MethodGet, gatewayCore+"/secrets/openshell-client-tls", nil)
	if err != nil {
		return err
	}
	if code == 404 {
		return ErrPending
	}
	if !owner(id).Matches(secret) {
		return errors.New("Sandbox TLS Secret has a different owner")
	}
	crt, err := data(secret, "tls.crt")
	if err != nil {
		return err
	}
	key, err := data(secret, "tls.key")
	if err != nil {
		return err
	}
	pair, err := tls.X509KeyPair(crt, key)
	if err != nil {
		return errors.New("Sandbox TLS key does not match its certificate")
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return err
	}
	intermediates := x509.NewCertPool()
	for _, raw := range pair.Certificate[1:] {
		cert, err := x509.ParseCertificate(raw)
		if err != nil {
			return err
		}
		intermediates.AddCert(cert)
	}
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}); err != nil {
		return errors.New("Sandbox TLS client certificate is invalid")
	}
	desired := definition("v1", "Secret", "openshell-client-tls", id)
	desired["type"] = "kubernetes.io/tls"
	values := object{}
	for _, name := range []string{"tls.crt", "tls.key", "ca.crt"} {
		if _, err := data(secret, name); err != nil {
			return err
		}
		values[name] = kube.String(secret, "data", name)
	}
	desired["data"] = values
	_, err = k.ensure(ctx, core+"/secrets", desired, id)
	return err
}

func sandboxRestrictedNamespace(ns object) object {
	ns["metadata"].(object)["labels"].(object)["pod-security.kubernetes.io/enforce"] = "restricted"
	return ns
}

// The pinned driver uses a disk volume for its Unix socket and runs workspace
// setup as root. Adapt these fields before the Pod can start. Validation still
// applies to the final Pod. No caller can use this policy to choose a runtime.
func sandboxMutation(id, ns string) []resource {
	policy := definition("admissionregistration.k8s.io/v1beta1", "MutatingAdmissionPolicy", ns, id)
	policy["spec"] = object{
		"failurePolicy": "Fail", "reinvocationPolicy": "IfNeeded",
		"matchConstraints": object{"resourceRules": []object{{"apiGroups": []string{""}, "apiVersions": []string{"v1"}, "operations": []string{"CREATE"}, "resources": []string{"pods"}}}},
		"matchConditions":  []object{{"name": "sandbox-namespace", "expression": fmt.Sprintf("request.namespace == %q", ns)}},
		"mutations": []object{
			{"patchType": "JSONPatch", "jsonPatch": object{"expression": `has(object.spec.volumes) && object.spec.volumes.exists(v, v.name == 'openshell-sidecar-state' && has(v.emptyDir)) ? [JSONPatch{op: 'add', path: '/spec/volumes/' + string(object.spec.volumes.map(v, v.name).indexOf('openshell-sidecar-state')) + '/emptyDir', value: {'medium': 'Memory', 'sizeLimit': '16Mi'}}] : []`}},
			{"patchType": "JSONPatch", "jsonPatch": object{"expression": `has(object.spec.initContainers) && object.spec.initContainers.exists(c, c.name == 'workspace-init') && object.spec.containers.exists(c, c.name == 'agent' && has(c.securityContext) && has(c.securityContext.runAsUser)) ? [JSONPatch{op: 'add', path: '/spec/initContainers/' + string(object.spec.initContainers.map(c, c.name).indexOf('workspace-init')) + '/securityContext', value: Object.spec.initContainers.securityContext{runAsNonRoot: true, runAsUser: object.spec.containers.filter(c, c.name == 'agent')[0].securityContext.runAsUser, allowPrivilegeEscalation: false, capabilities: Object.spec.initContainers.securityContext.capabilities{drop: ['ALL']}}}] : []`}},
		},
	}
	binding := definition("admissionregistration.k8s.io/v1beta1", "MutatingAdmissionPolicyBinding", ns, id)
	binding["spec"] = object{"policyName": ns}
	return []resource{{mutationAPI + "/mutatingadmissionpolicies", policy}, {mutationAPI + "/mutatingadmissionpolicybindings", binding}}
}

func sandboxSocketInMemory(pod object) bool {
	spec, _ := pod["spec"].(map[string]any)
	volumes, _ := spec["volumes"].([]any)
	for _, value := range volumes {
		volume, _ := value.(map[string]any)
		if kube.String(volume, "name") == "openshell-sidecar-state" {
			return kube.String(volume, "emptyDir", "medium") == "Memory" && kube.String(volume, "emptyDir", "sizeLimit") == "16Mi"
		}
	}
	return false
}
