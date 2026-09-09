package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"
)

// ReconcileGatewayUser changes only roles for this managed Gateway client.
// The subject is the provider user ID. A profile name is never a lookup key.
func (c *Client) ReconcileGatewayUser(ctx context.Context, id, issuer, subject, role string) error {
	if issuer != c.issuer() {
		return errors.New("Gateway user issuer does not match the provider")
	}
	if subject == "" || len(subject) > 255 || !utf8.ValidString(subject) || strings.ContainsAny(subject, "/\\\x00\r\n") {
		return errors.New("invalid Gateway user subject")
	}
	providerRole := ""
	switch role {
	case "gateway:owner":
		providerRole = RoleAdmin
	case "gateway:viewer":
		providerRole = RoleUser
	case "":
	default:
		return errors.New("invalid Gateway user role")
	}
	clientID, err := GatewayClientID(id)
	if err != nil {
		return err
	}
	gatewayUUID, roles, err := c.resolveGatewayRoles(ctx, clientID, id, providerRole)
	if err != nil {
		return err
	}
	if role == "" {
		roles = nil
	}
	userPath := fmt.Sprintf("/admin/realms/%s/users/%s", c.realm, url.PathEscape(subject))
	body, code, err := c.admin(ctx, http.MethodGet, userPath, nil)
	if err != nil {
		return err
	}
	if code == http.StatusNotFound && role == "" {
		return nil
	}
	if code != http.StatusOK {
		return statusError("read Gateway user", code)
	}
	var user struct {
		ID                     string `json:"id"`
		Enabled                bool   `json:"enabled"`
		ServiceAccountClientID string `json:"serviceAccountClientId"`
	}
	if json.Unmarshal(body, &user) != nil || user.ID != subject || user.ServiceAccountClientID != "" {
		return errors.New("Gateway user identity is not a human provider user")
	}
	if !user.Enabled && role != "" {
		return errors.New("Gateway user is disabled")
	}
	path := userPath + "/role-mappings/clients/" + url.PathEscape(gatewayUUID)
	read := func(suffix string) ([]kcRole, error) {
		body, code, err := c.admin(ctx, http.MethodGet, path+suffix, nil)
		if err != nil {
			return nil, err
		}
		if code != http.StatusOK {
			return nil, statusError("read Gateway user roles", code)
		}
		var current []kcRole
		if json.Unmarshal(body, &current) != nil {
			return nil, errors.New("invalid Gateway user roles")
		}
		for _, r := range current {
			if r.ID == "" || r.Name == "" {
				return nil, errors.New("incomplete Gateway role")
			}
		}
		return current, nil
	}
	current, err := read("")
	if err != nil {
		return err
	}
	contains := func(set []kcRole, item kcRole) bool {
		for _, r := range set {
			if r.ID == item.ID && r.Name == item.Name {
				return true
			}
		}
		return false
	}
	var remove, add []kcRole
	for _, r := range current {
		if !contains(roles, r) {
			remove = append(remove, r)
		}
	}
	for _, r := range roles {
		if !contains(current, r) {
			add = append(add, r)
		}
	}
	// Remove excess access before adding access. Preserve other clients and realm roles.
	for _, operation := range []struct {
		method string
		roles  []kcRole
	}{{http.MethodDelete, remove}, {http.MethodPost, add}} {
		if len(operation.roles) == 0 {
			continue
		}
		payload, err := json.Marshal(operation.roles)
		if err != nil {
			return err
		}
		_, code, err := c.admin(ctx, operation.method, path, payload)
		if err != nil {
			return err
		}
		if code != http.StatusNoContent {
			return statusError("change Gateway user roles", code)
		}
	}
	current, err = read("/composite")
	if err != nil {
		return err
	}
	if !sameRoleSet(current, roles) {
		return errors.New("effective Gateway user roles do not match current grants")
	}
	return nil
}
