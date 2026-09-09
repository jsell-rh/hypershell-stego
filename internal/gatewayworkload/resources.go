package gatewayworkload

import (
	"encoding/hex"
	"encoding/json"
	"fmt"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
)

func configuration(ns string, o Options) string {
	return fmt.Sprintf(`[openshell]
version = 1
[openshell.gateway]
bind_address = "0.0.0.0:8080"
health_bind_address = "0.0.0.0:8081"
metrics_bind_address = "0.0.0.0:9090"
log_level = "info"
sandbox_namespace = %q
default_image = %q
supervisor_image = %q
client_tls_secret_name = "openshell-client-tls"
enable_loopback_service_http = true
policy_validation_failure_mode = "fail_closed"
server_sans = ["openshell-gateway.%s.svc.cluster.local"]
[openshell.gateway.tls]
cert_path = "/etc/openshell-tls/tls.crt"
key_path = "/etc/openshell-tls/tls.key"
[openshell.gateway.auth]
allow_unauthenticated_users = false
[openshell.gateway.credential_storage]
key_encryption_key_env = "OPENSHELL_GATEWAY_CREDENTIAL_KEY_ENCRYPTION_KEY"
[openshell.gateway.gateway_jwt]
signing_key_path = "/etc/openshell-jwt/signing.pem"
public_key_path = "/etc/openshell-jwt/public.pem"
kid_path = "/etc/openshell-jwt/kid"
gateway_id = "openshell-gateway"
ttl_secs = 3600
[openshell.drivers.kubernetes]
grpc_endpoint = "https://openshell-gateway.%s.svc.cluster.local:8080"
service_account_name = "openshell-gateway-sandbox"
supervisor_sideload_method = "image-volume"
sa_token_ttl_secs = 3600
app_armor_profile = "Unconfined"
topology = "combined"
[openshell.drivers.kubernetes.sidecar]
proxy_uid = 1337
process_binary_aware_network_policy = false
`, ns, o.SandboxImage, o.SupervisorImage, ns, ns)
}

type resource struct {
	path   string
	object object
}

func resources(gw *pb.Gateway, release *pb.GatewayRelease, oidc oidcConfig, config, dbData, keys object, certificateHash string) []resource {
	id, ns := gw.Metadata.Id, gw.Namespace
	core := "/api/v1/namespaces/" + ns
	rbac := "/apis/rbac.authorization.k8s.io/v1"
	result := []resource{}
	add := func(path string, o object) { result = append(result, resource{path, o}) }
	for _, name := range []string{Name, Name + "-sandbox"} {
		add(core+"/serviceaccounts", definition("v1", "ServiceAccount", name, id))
	}
	clusterRole := definition("rbac.authorization.k8s.io/v1", "ClusterRole", ns, id)
	clusterRole["rules"] = []object{{"apiGroups": []string{"authentication.k8s.io"}, "resources": []string{"tokenreviews"}, "verbs": []string{"create"}}, {"apiGroups": []string{""}, "resources": []string{"nodes"}, "verbs": []string{"get", "list", "watch"}}, {"apiGroups": []string{""}, "resources": []string{"namespaces"}, "verbs": []string{"get"}}}
	add(rbac+"/clusterroles", clusterRole)
	binding := definition("rbac.authorization.k8s.io/v1", "ClusterRoleBinding", ns, id)
	binding["roleRef"] = object{"apiGroup": "rbac.authorization.k8s.io", "kind": "ClusterRole", "name": ns}
	binding["subjects"] = []object{{"kind": "ServiceAccount", "name": Name, "namespace": ns}}
	add(rbac+"/clusterrolebindings", binding)
	role := definition("rbac.authorization.k8s.io/v1", "Role", Name+"-sandbox", id)
	role["rules"] = []object{{"apiGroups": []string{"agents.x-k8s.io"}, "resources": []string{"sandboxes", "sandboxes/status"}, "verbs": []string{"get", "list", "watch", "create", "update", "patch", "delete"}}, {"apiGroups": []string{""}, "resources": []string{"events"}, "verbs": []string{"get", "list", "watch"}}, {"apiGroups": []string{""}, "resources": []string{"pods"}, "verbs": []string{"get"}}}
	add(rbac+"/namespaces/"+ns+"/roles", role)
	binding = definition("rbac.authorization.k8s.io/v1", "RoleBinding", Name+"-sandbox", id)
	binding["roleRef"] = object{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": Name + "-sandbox"}
	binding["subjects"] = []object{{"kind": "ServiceAccount", "name": Name, "namespace": ns}}
	add(rbac+"/namespaces/"+ns+"/rolebindings", binding)
	service := definition("v1", "Service", Name, id)
	service["spec"] = object{"type": "ClusterIP", "selector": object{ownerLabel: id}, "ports": []object{{"name": "grpc", "port": 8080, "targetPort": "grpc"}}}
	add(core+"/services", service)
	secretEnv := func(name, secret, key string) object {
		return object{"name": name, "valueFrom": object{"secretKeyRef": object{"name": secret, "key": key}}}
	}
	env := []object{secretEnv("OPENSHELL_DB_URL", "openshell-gateway-db-credentials", "uri"), secretEnv("OPENSHELL_GATEWAY_CREDENTIAL_KEY_ENCRYPTION_KEY", keysName, "key-encryption-key")}
	for _, pair := range [][2]string{{"SSL_CERT_FILE", "/etc/openshell-config/trust.pem"}, {"OPENSHELL_OIDC_ISSUER", oidc.Issuer}, {"OPENSHELL_OIDC_AUDIENCE", oidc.Audience}, {"OPENSHELL_OIDC_ROLES_CLAIM", oidc.RolesClaim}, {"OPENSHELL_OIDC_ADMIN_ROLE", oidc.AdminRole}, {"OPENSHELL_OIDC_USER_ROLE", oidc.UserRole}} {
		env = append(env, object{"name": pair[0], "value": pair[1]})
	}
	volumes := []object{{"name": "tmp", "emptyDir": object{"sizeLimit": "64Mi"}}, {"name": "config", "configMap": object{"name": Name + "-config"}}}
	mounts := []object{{"name": "tmp", "mountPath": "/tmp"}, {"name": "config", "mountPath": "/etc/openshell-config", "readOnly": true}}
	for _, item := range [][3]string{{"tls", "openshell-server-tls", "/etc/openshell-tls"}, {"keys", keysName, "/etc/openshell-jwt"}, {"database", "openshell-gateway-db-credentials", "/etc/openshell-db"}} {
		volumes = append(volumes, object{"name": item[0], "secret": object{"secretName": item[1], "defaultMode": int32(0440)}})
		mounts = append(mounts, object{"name": item[0], "mountPath": item[2], "readOnly": true})
	}
	container := object{"name": Name, "image": release.GetImage(), "imagePullPolicy": "IfNotPresent", "args": []string{"--config", "/etc/openshell-config/gateway.toml", "--drivers", "kubernetes"}, "env": env, "ports": []object{{"name": "grpc", "containerPort": 8080}, {"name": "health", "containerPort": 8081}}, "volumeMounts": mounts, "securityContext": object{"allowPrivilegeEscalation": false, "readOnlyRootFilesystem": true, "capabilities": object{"drop": []string{"ALL"}}}, "resources": object{"requests": object{"cpu": "100m", "memory": "256Mi"}, "limits": object{"cpu": "500m", "memory": "512Mi"}}, "readinessProbe": object{"httpGet": object{"path": "/readyz", "port": "health"}, "periodSeconds": 2}, "livenessProbe": object{"httpGet": object{"path": "/healthz", "port": "health"}, "periodSeconds": 10}, "startupProbe": object{"httpGet": object{"path": "/healthz", "port": "health"}, "periodSeconds": 2, "failureThreshold": 60}}
	encoded, _ := json.Marshal([]any{config, dbData, keys, oidc, certificateHash})
	deployment := definition("apps/v1", "Deployment", Name, id)
	deployment["spec"] = object{"replicas": 1, "strategy": object{"type": "Recreate"}, "selector": object{"matchLabels": object{ownerLabel: id}}, "template": object{"metadata": object{"labels": object{ownerLabel: id}, "annotations": object{"hypershell.redhat.io/config-sha256": hex.EncodeToString(sha256sum(encoded))}}, "spec": object{"serviceAccountName": Name, "automountServiceAccountToken": true, "securityContext": object{"runAsNonRoot": true, "runAsUser": 1000, "runAsGroup": 1000, "fsGroup": 1000, "seccompProfile": object{"type": "RuntimeDefault"}}, "containers": []object{container}, "volumes": volumes}}}
	add("/apis/apps/v1/namespaces/"+ns+"/deployments", deployment)
	return result
}
