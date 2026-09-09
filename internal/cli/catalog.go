package cli

import command "github.com/jsell-rh/hypershell-stego/out/cli/command"

func catalogCommands() []command.Command {
	resources := []struct {
		name, alias, path string
		fields            []command.Field
	}{
		{"gatewayNetwork", "gateway-network", "gateway_networks", []command.Field{
			{Flag: "name", Key: "name", Type: "string"},
			{Flag: "topology", Key: "topology", Type: "string", Nullable: true},
			{Flag: "tunnel-mode", Key: "tunnel_mode", Type: "string", Nullable: true},
			{Flag: "hub-gateway-id", Key: "hub_gateway_id", Type: "string", Nullable: true},
			{Flag: "status", Key: "status", Type: "string", Nullable: true},
		}},
		{"managedCluster", "managed-cluster", "managed_clusters", []command.Field{
			{Flag: "name", Key: "name", Type: "string"},
			{Flag: "provider", Key: "provider", Type: "string"},
			{Flag: "region", Key: "region", Type: "string", Nullable: true},
			{Flag: "kubeconfig-secret", Key: "kubeconfig_secret", Type: "string"},
			{Flag: "status", Key: "status", Type: "string", Nullable: true},
			{Flag: "api-server-url", Key: "api_server_url", Type: "string", Nullable: true},
		}},
		{"gatewayRelease", "gateway-release", "gateway_releases", []command.Field{
			{Flag: "name", Key: "name", Type: "string"},
			{Flag: "image", Key: "image", Type: "string"},
			{Flag: "rollout-strategy", Key: "rollout_strategy", Type: "string", Nullable: true},
			{Flag: "canary-percent", Key: "canary_percent", Type: "integer", Nullable: true},
			{Flag: "canary-duration", Key: "canary_duration", Type: "string", Nullable: true},
			{Flag: "status", Key: "status", Type: "string", Nullable: true},
		}},
		{"managedDatabase", "managed-database", "managed_databases", []command.Field{
			{Flag: "name", Key: "name", Type: "string"},
			{Flag: "provider", Key: "provider", Type: "string"},
			{Flag: "region", Key: "region", Type: "string", Nullable: true},
			{Flag: "engine", Key: "engine", Type: "string", Nullable: true},
			{Flag: "engine-version", Key: "engine_version", Type: "string", Nullable: true},
			{Flag: "instance-class", Key: "instance_class", Type: "string", Nullable: true},
			{Flag: "connection-secret", Key: "connection_secret", Type: "string", Nullable: true},
			{Flag: "status", Key: "status", Type: "string", Nullable: true},
		}},
	}
	query := []command.Field{{Flag: "page", Key: "page", Type: "integer"}, {Flag: "size", Key: "size", Type: "integer"}, {Flag: "search", Key: "search", Type: "string"}, {Flag: "order-by", Key: "orderBy", Type: "string"}}
	var commands []command.Command
	for _, resource := range resources {
		path := "/api/hypershell/v1/" + resource.path
		for _, name := range []string{resource.name, resource.alias} {
			commands = append(commands,
				command.Command{Name: []string{"create", name}, Method: "POST", Path: path, Fields: resource.fields, Success: []int{201}},
				command.Command{Name: []string{"delete", name}, Method: "DELETE", Path: path + "/{id}", ID: true, Confirm: true, Success: []int{204}},
			)
			for _, alias := range []string{name, name + "s"} {
				commands = append(commands,
					command.Command{Name: []string{"get", alias}, Method: "GET", Path: path + "/{id}", ID: true, Success: []int{200}},
					command.Command{Name: []string{"list", alias}, Method: "GET", Path: path, Query: true, Fields: query, Success: []int{200}},
				)
			}
		}
	}
	return commands
}
