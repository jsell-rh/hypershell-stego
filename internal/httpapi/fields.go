package httpapi

import "github.com/jsell-rh/hypershell-stego/out/application/transport"

// These are public response names, not storage columns. The generated projector
// runs after access checks and presentation. Undeclared data stays private.
var listFieldProjectors, listFieldError = buildListFieldProjectors()

func buildListFieldProjectors() (map[string]*transport.FieldProjector, error) {
	fields := map[string][]string{
		"Gateway":         {"name", "cluster_id", "release_id", "database_id", "namespace", "external_dns", "tls_mode", "service_type", "status", "phase", "image", "supervisor_image", "server_dns_names", "route_address", "console_address", "oidc", "route", "credential_driver", "active_sandbox_count", "created_by"},
		"GatewayNetwork":  {"name", "topology", "tunnel_mode", "hub_gateway_id", "status"},
		"ManagedCluster":  {"name", "provider", "region", "kubeconfig_secret", "status", "api_server_url"},
		"GatewayRelease":  {"name", "image", "rollout_strategy", "canary_percent", "canary_duration", "status"},
		"ManagedDatabase": {"name", "provider", "namespace", "region", "engine", "engine_version", "instance_class", "connection_secret", "status"},
		"Role":            {"name", "display_name", "description", "permissions", "built_in"},
		"RoleBinding":     {"role_id", "user_id", "gateway_id", "scope"},
	}
	result := make(map[string]*transport.FieldProjector, len(fields))
	for entity, names := range fields {
		schema := transport.FieldSchema{"id": nil, "kind": nil, "href": nil, "created_at": nil, "updated_at": nil}
		for _, name := range names {
			schema[name] = nil
		}
		projector, err := transport.NewFieldProjector(schema)
		if err != nil {
			return nil, err
		}
		result[entity] = projector
	}
	return result, nil
}
