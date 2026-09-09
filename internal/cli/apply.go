package cli

import command "github.com/jsell-rh/hypershell-stego/out/cli/command"

// Apply uses the same public fields as the resource commands. Patch has its
// own contract. Placement and access checks remain in the API domain service.
func applyResources(commands []command.Command) []command.ApplyResource {
	types := []struct {
		kind, path string
		required   []string
	}{
		{"Gateway", "gateways", []string{"name", "cluster_id", "release_id", "database_id"}},
		{"ManagedCluster", "managed_clusters", []string{"name", "provider", "kubeconfig_secret"}},
		{"GatewayRelease", "gateway_releases", []string{"name", "image"}},
		{"ManagedDatabase", "managed_databases", []string{"name", "provider"}},
		{"GatewayNetwork", "gateway_networks", []string{"name"}},
	}
	var resources []command.ApplyResource
	for _, resource := range types {
		path := "/api/hypershell/v1/" + resource.path
		for _, definition := range commands {
			if definition.Method != "POST" || definition.Path != path {
				continue
			}
			create := append([]command.Field(nil), definition.Fields...)
			patch := append([]command.Field(nil), definition.Fields...)
			for i := range create {
				for _, required := range resource.required {
					if create[i].Key == required {
						create[i].Required = true
						create[i].Nullable = false
					}
				}
				patch[i].Required = false
				patch[i].Nullable = true
			}
			if resource.kind == "Gateway" {
				patch = append(patch, command.Field{Flag: "route-address", Key: "route_address", Type: "string", Nullable: true})
			}
			resources = append(resources, command.ApplyResource{Kind: resource.kind, APIVersion: "hypershell/v1", Path: path, CreateFields: create, PatchFields: patch})
			break
		}
	}
	return resources
}
