package cli

import command "github.com/jsell-rh/hypershell-stego/out/cli/command"

func grantCommands() []command.Command {
	path := "/api/hypershell/v1/role_bindings"
	fields := []command.Field{
		{Flag: "gateway-id", Key: "gateway_id", Type: "string", Required: true},
		{Flag: "role-id", Key: "role_id", Type: "string", Required: true},
		{Flag: "scope", Key: "scope", Type: "string", Required: true},
		{Flag: "user-id", Key: "user_id", Type: "string", Required: true},
	}
	query := []command.Field{
		{Flag: "page", Key: "page", Type: "integer"},
		{Flag: "size", Key: "size", Type: "integer"},
		{Flag: "search", Key: "search", Type: "string"},
		{Flag: "order-by", Key: "orderBy", Type: "string"},
	}
	commands := []command.Command{{Name: []string{"get", "current-user"}, Method: "GET", Path: "/api/hypershell/v1/users/me", Success: []int{200}}}
	for _, alias := range []string{"roleBinding", "role-binding"} {
		commands = append(commands,
			command.Command{Name: []string{"create", alias}, Method: "POST", Path: path, Fields: fields, Success: []int{201}},
			command.Command{Name: []string{"delete", alias}, Method: "DELETE", Path: path + "/{id}", ID: true, Confirm: true, Success: []int{204}},
		)
	}
	for _, alias := range []string{"roleBinding", "roleBindings", "role-binding", "role-bindings"} {
		commands = append(commands,
			command.Command{Name: []string{"get", alias}, Method: "GET", Path: path + "/{id}", ID: true, Success: []int{200}},
			command.Command{Name: []string{"list", alias}, Method: "GET", Path: path, Query: true, Fields: query, Success: []int{200}},
		)
	}
	for _, alias := range []string{"role", "roles"} {
		commands = append(commands,
			command.Command{Name: []string{"get", alias}, Method: "GET", Path: "/api/hypershell/v1/roles/{id}", ID: true, Success: []int{200}},
			command.Command{Name: []string{"list", alias}, Method: "GET", Path: "/api/hypershell/v1/roles", Query: true, Fields: query, Success: []int{200}},
		)
	}
	return commands
}
