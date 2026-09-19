package gatewayworkload

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net/http"

	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
)

// ensureSandbox uses the generated namespace and account checks. The workload
// client does not create namespaces, accounts, or admission policy.
func (k *Kubernetes) ensureSandbox(ctx context.Context, id, ns, gatewayCore string, roots *x509.CertPool) (string, error) {
	if k.allocation == nil {
		return "", errors.New("Sandbox requires namespace allocation")
	}
	if err := k.allocation.RequireNamespace(ctx, "sandbox", ns, id); err != nil {
		return "", err
	}
	account, err := k.allocation.RequireServiceAccount(ctx, "sandbox", ns, id, "sandbox")
	if err != nil {
		return "", err
	}
	if err := k.checkSandboxAdmission(ctx, id, ns, account); err != nil {
		return "", err
	}
	if err := k.copySandboxIdentity(ctx, id, ns, gatewayCore, roots); err != nil {
		return "", err
	}
	return account, nil
}

// Check the operator's selected runtime and images before publishing them in
// Gateway configuration. All Pod requests use server dry run.
func (k *Kubernetes) checkSandboxAdmission(ctx context.Context, id, ns, account string) error {
	path := "/api/v1/namespaces/" + ns + "/pods?dryRun=All"
	expected := sandboxProbe(id, ns, account, k.options)
	admitted, _, err := k.client.Request(ctx, http.MethodPost, path, expected)
	if err != nil {
		return err
	}
	if !sandboxProbeUnchanged(admitted, expected) {
		return errors.New("Admission changed the Sandbox setup probe")
	}
	rejected := sandboxProbe(id, ns, account, k.options)
	delete(rejected["spec"].(object), "runtimeClassName")
	_, code, err := k.client.Request(ctx, http.MethodPost, path, rejected)
	if err == nil || (code != http.StatusForbidden && code != http.StatusUnprocessableEntity) {
		return errors.New("Sandbox runtime rejection was not confirmed")
	}
	return nil
}

func sandboxProbeUnchanged(admitted, expected object) bool {
	if !kube.Contains(admitted, expected) {
		return false
	}
	volumes, ok := kube.Nested(admitted, "spec", "volumes").([]any)
	if !ok {
		return false
	}
	for _, value := range volumes {
		volume, ok := value.(map[string]any)
		if !ok {
			return false
		}
		if volume["name"] == "socket-state" {
			directory, ok := volume["emptyDir"].(map[string]any)
			if !ok {
				return false
			}
			if medium, present := directory["medium"]; present && medium != "" {
				return false
			}
		}
	}
	for _, field := range []string{"containers", "initContainers"} {
		containers, ok := kube.Nested(admitted, "spec", field).([]any)
		if !ok {
			return false
		}
		for _, value := range containers {
			container, ok := value.(map[string]any)
			if !ok {
				return false
			}
			if !sandboxEnvironmentAllowed(container) {
				return false
			}
			if container["name"] == "agent" || container["name"] == "workspace-init" {
				if mounts, present := container["volumeMounts"]; present {
					values, ok := mounts.([]any)
					if !ok || len(values) != 0 {
						return false
					}
				}
			}
		}
	}
	return true
}

func sandboxEnvironmentAllowed(container map[string]any) bool {
	for _, field := range []string{"env", "envFrom"} {
		raw, present := container[field]
		if !present {
			continue
		}
		entries, ok := raw.([]any)
		if !ok {
			return false
		}
		for _, raw := range entries {
			entry, ok := raw.(map[string]any)
			if !ok {
				return false
			}
			if field == "envFrom" {
				if _, secret := entry["secretRef"]; secret {
					return false
				}
			} else if raw, present := entry["valueFrom"]; present {
				source, ok := raw.(map[string]any)
				if !ok {
					return false
				}
				if _, secret := source["secretKeyRef"]; secret {
					return false
				}
			}
		}
	}
	return true
}

func sandboxProbe(id, ns, account string, options Options) object {
	probe := definition("v1", "Pod", "sandbox-setup-check", id)
	probe["metadata"].(object)["namespace"] = ns
	resources := func() object {
		return object{"requests": object{"cpu": "10m", "memory": "16Mi", "ephemeral-storage": "16Mi"}, "limits": object{"cpu": "100m", "memory": "64Mi", "ephemeral-storage": "32Mi"}}
	}
	helper := func(name string, caps []string) object {
		return object{"name": name, "image": options.SupervisorImage, "resources": resources(), "securityContext": object{"runAsUser": 0, "allowPrivilegeEscalation": false, "capabilities": object{"drop": []string{"ALL"}, "add": caps}}}
	}
	network := helper("openshell-supervisor-network", []string{"SYS_PTRACE", "DAC_READ_SEARCH"})
	network["volumeMounts"] = []object{{"name": "openshell-client-tls", "mountPath": "/identity", "readOnly": true}}
	probe["spec"] = object{
		"runtimeClassName": options.SandboxRuntimeClass, "serviceAccountName": account, "automountServiceAccountToken": false, "restartPolicy": "Never",
		"securityContext": object{"seccompProfile": object{"type": "RuntimeDefault"}},
		"containers": []object{
			{"name": "agent", "image": options.SandboxImage, "resources": resources(), "securityContext": object{"runAsUser": 1000, "runAsNonRoot": true, "allowPrivilegeEscalation": false, "capabilities": object{"drop": []string{"ALL"}}}},
			network,
		},
		"initContainers": []object{
			{"name": "workspace-init", "image": options.SandboxImage, "resources": resources(), "securityContext": object{"runAsUser": 0}},
			helper("openshell-network-init", []string{"NET_ADMIN", "NET_RAW", "CHOWN", "FOWNER"}),
		},
		"volumes": []object{{"name": "socket-state", "emptyDir": object{}}, {"name": "openshell-client-tls", "secret": object{"secretName": "openshell-client-tls"}}},
	}
	return probe
}

func (k *Kubernetes) copySandboxIdentity(ctx context.Context, id, ns, gatewayCore string, roots *x509.CertPool) error {
	core := "/api/v1/namespaces/" + ns
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
