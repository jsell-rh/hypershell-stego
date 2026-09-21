package gatewayworkload

import (
	"fmt"

	pb "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/v1"
	kube "github.com/jsell-rh/hypershell-stego/out/kubernetes"
	workload "github.com/jsell-rh/hypershell-stego/out/workload"
)

func configuration(ns, sandboxNS, sandboxAccount string, o Options) string {
	topology := "combined"
	if o.SandboxRuntimeClass != "" {
		topology = "sidecar"
	}
	publicTLS := ""
	if o.PublicDomain != "" {
		publicTLS = fmt.Sprintf(`external_cert_path = "/etc/openshell-public-tls/tls.crt"
external_key_path = "/etc/openshell-public-tls/tls.key"
external_server_names = [%q]
`, publicHostname(ns, o.PublicDomain))
	}
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
%s[openshell.gateway.auth]
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
service_account_name = %q
supervisor_sideload_method = "image-volume"
default_runtime_class_name = %q
sa_token_ttl_secs = 3600
app_armor_profile = "Unconfined"
topology = %q
[openshell.drivers.kubernetes.sidecar]
proxy_uid = 1337
process_binary_aware_network_policy = true
`, sandboxNS, o.SandboxImage, o.SupervisorImage, ns, publicTLS, ns, sandboxAccount, o.SandboxRuntimeClass, topology)
}

type resource struct {
	path   string
	object object
}

func resources(gw *pb.Gateway, serviceAccount string, release *pb.GatewayRelease, oidc oidcConfig, config, dbData, keys, server, publicServer object) ([]resource, error) {
	id, ns := gw.Metadata.Id, gw.Namespace
	env := []workload.Env{
		{Name: "OPENSHELL_DB_URL", Secret: "openshell-gateway-db-credentials", Key: "uri"},
		{Name: "OPENSHELL_GATEWAY_CREDENTIAL_KEY_ENCRYPTION_KEY", Secret: keysName, Key: "key-encryption-key"},
	}
	for _, pair := range [][2]string{{"SSL_CERT_FILE", "/etc/openshell-config/trust.pem"}, {"OPENSHELL_OIDC_ISSUER", oidc.Issuer}, {"OPENSHELL_OIDC_AUDIENCE", oidc.Audience}, {"OPENSHELL_OIDC_ROLES_CLAIM", oidc.RolesClaim}, {"OPENSHELL_OIDC_ADMIN_ROLE", oidc.AdminRole}, {"OPENSHELL_OIDC_USER_ROLE", oidc.UserRole}} {
		env = append(env, workload.Env{Name: pair[0], Value: pair[1]})
	}
	volumes := []workload.Volume{{Name: "tmp", EmptyMi: 64}, {Name: "config", ConfigMap: Name + "-config"}}
	mounts := []workload.Mount{{Name: "tmp", Path: "/tmp"}, {Name: "config", Path: "/etc/openshell-config"}}
	secrets := []struct {
		name, volume, path string
		data               map[string]any
	}{
		{"openshell-server-tls", "tls", "/etc/openshell-tls", kube.NestedMap(server, "data")},
		{keysName, "keys", "/etc/openshell-jwt", keys},
		{"openshell-gateway-db-credentials", "database", "/etc/openshell-db", dbData},
	}
	if publicServer != nil {
		secrets = append(secrets, struct {
			name, volume, path string
			data               map[string]any
		}{"openshell-public-tls", "public-tls", "/etc/openshell-public-tls", kube.NestedMap(publicServer, "data")})
	}
	configuration, err := workload.DependencyFromData("ConfigMap", Name+"-config", kube.NestedMap(config, "data"))
	if err != nil {
		return nil, err
	}
	dependencies := []workload.Dependency{configuration}
	for _, secret := range secrets {
		dependency, err := workload.DependencyFromData("Secret", secret.name, secret.data)
		if err != nil {
			return nil, err
		}
		dependencies = append(dependencies, dependency)
		volumes = append(volumes, workload.Volume{Name: secret.volume, Secret: secret.name})
		mounts = append(mounts, workload.Mount{Name: secret.volume, Path: secret.path})
	}
	built, err := workload.Build(workload.Deployment{
		Name: Name, Namespace: ns, ServiceAccount: serviceAccount,
		OwnerLabels: map[string]string{ownerLabel: id, managerLabel: manager},
		PodLabels:   map[string]string{ownerLabel: id, "app.kubernetes.io/name": Name},
		Selector:    map[string]string{ownerLabel: id},
		Replicas:    1, Strategy: "Recreate", RunAsUser: 1000, RunAsGroup: 1000, FSGroup: 1000,
		KubernetesAPI: true, TerminationGraceSeconds: 30,
		Volumes: volumes, Dependencies: dependencies,
		ServicePorts: []workload.ServicePort{{Name: "grpc", Port: 8080, Target: "grpc"}},
		Containers: []workload.Container{{
			Name: Name, Image: release.GetImage(),
			Args: []string{"--config", "/etc/openshell-config/gateway.toml", "--drivers", "kubernetes"},
			Env:  env, Mounts: mounts,
			Ports:     []workload.Port{{Name: "grpc", Number: 8080}, {Name: "health", Number: 8081}},
			Requests:  workload.Resources{CPUMilli: 100, MemoryMi: 256, EphemeralMi: 32},
			Limits:    workload.Resources{CPUMilli: 500, MemoryMi: 512, EphemeralMi: 256},
			Startup:   workload.HTTPProbe{Path: "/healthz", Port: "health", PeriodSeconds: 2, TimeoutSeconds: 1, FailureThreshold: 60},
			Readiness: workload.HTTPProbe{Path: "/readyz", Port: "health", PeriodSeconds: 2, TimeoutSeconds: 1, FailureThreshold: 3},
			Liveness:  workload.HTTPProbe{Path: "/healthz", Port: "health", PeriodSeconds: 10, TimeoutSeconds: 1, FailureThreshold: 3},
		}},
	})
	if err != nil {
		return nil, err
	}
	result := make([]resource, 0, len(built))
	for _, entry := range built {
		result = append(result, resource{entry.Collection, object(entry.Object)})
	}
	return result, nil
}
