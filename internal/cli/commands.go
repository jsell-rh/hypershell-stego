// Package cli declares the Hypershell command names and public request fields.
package cli

import (
	command "github.com/jsell-rh/hypershell-stego/out/cli/command"
	"strings"
)

func Commands() command.Application {
	fields := []command.Field{
		{Flag: "name", Key: "name", Type: "string", Required: true},
		{Flag: "cluster-id", Key: "cluster_id", Type: "string", Required: true},
		{Flag: "release-id", Key: "release_id", Type: "string", Required: true},
		{Flag: "database-id", Key: "database_id", Type: "string", Required: true},
		{Flag: "server-dns-names", Key: "server_dns_names", Type: "string-list", Nullable: true},
	}
	for _, name := range []string{"external_dns", "tls_mode", "service_type", "status", "phase", "image", "supervisor_image", "oidc", "route", "credential_driver"} {
		flag := strings.ReplaceAll(name, "_", "-")
		fields = append(fields, command.Field{Flag: flag, Key: name, Type: "string", Nullable: true})
	}
	path := "/api/hypershell/v1/gateways"
	app := command.Application{ConfigEnv: "HYPERSHELL_CONFIG", ConfigName: "hypershell", OIDCClientID: "hypershell-cli", Commands: []command.Command{
		{Name: []string{"create", "gateway"}, Method: "POST", Path: path, Fields: fields, Success: []int{201}},
		{Name: []string{"get", "gateway"}, Method: "GET", Path: path + "/{id}", ID: true, Success: []int{200}},
		{Name: []string{"get", "gateways"}, Method: "GET", Path: path + "/{id}", ID: true, Success: []int{200}},
		{Name: []string{"list", "gateways"}, Method: "GET", Path: path, Query: true, Fields: []command.Field{{Flag: "page", Key: "page", Type: "integer"}, {Flag: "size", Key: "size", Type: "integer"}, {Flag: "search", Key: "search", Type: "string"}, {Flag: "order-by", Key: "orderBy", Type: "string"}}, Success: []int{200}},
		{Name: []string{"delete", "gateway"}, Method: "DELETE", Path: path + "/{id}", ID: true, Confirm: true, Success: []int{204}},
	}}
	app.Commands = append(app.Commands, accountCommands()...)
	return app
}

func accountCommands() []command.Command {
	path := "/api/hypershell/v1/gateways/{gateway_id}/service_accounts"
	parameter := []command.PathParameter{{Flag: "gateway-id", Key: "gateway_id"}}
	fields := []command.Field{
		{Flag: "name", Key: "name", Type: "string", Required: true},
		{Flag: "description", Key: "description", Type: "string", Nullable: true},
		{Flag: "credential-type", Key: "credential_type", Type: "string"},
		{Flag: "role", Key: "role", Type: "string"},
		{Flag: "expires-at", Key: "expires_at", Type: "string", Nullable: true},
	}
	query := []command.Field{{Flag: "page", Key: "page", Type: "integer"}, {Flag: "size", Key: "size", Type: "integer"}}
	for _, name := range []string{"status", "search", "sort", "order"} {
		query = append(query, command.Field{Flag: name, Key: name, Type: "string"})
	}
	var commands []command.Command
	for _, alias := range []string{"service-account", "serviceAccount"} {
		commands = append(commands,
			command.Command{Name: []string{"create", alias}, Method: "POST", Path: path, PathParameters: parameter, Sensitive: true, Fields: fields, Success: []int{201}},
			command.Command{Name: []string{"get", alias}, Method: "GET", Path: path + "/{id}", PathParameters: parameter, ID: true, Success: []int{200}},
			command.Command{Name: []string{"revoke", alias}, Method: "POST", Path: path + "/{id}/revoke", PathParameters: parameter, ID: true, Success: []int{200, 202}},
			command.Command{Name: []string{"delete", alias}, Method: "DELETE", Path: path + "/{id}", PathParameters: parameter, ID: true, Confirm: true, Success: []int{202, 204}},
		)
	}
	for _, alias := range []string{"service-accounts", "serviceAccounts", "service-account", "serviceAccount"} {
		commands = append(commands, command.Command{Name: []string{"list", alias}, Method: "GET", Path: path, PathParameters: parameter, Query: true, Fields: query, Success: []int{200}})
	}
	return commands
}
