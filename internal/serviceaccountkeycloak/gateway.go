package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"reflect"

	"github.com/segmentio/ksuid"
)

const gatewayAttribute = "hypershell.gateway"

// GatewayClientID uses the immutable Gateway ID. A rename does not change tokens.
func GatewayClientID(id string) (string, error) {
	parsed, err := ksuid.Parse(id)
	if err != nil || parsed == ksuid.Nil || parsed.String() != id {
		return "", errors.New("invalid Gateway ID")
	}
	return "hs-gateway-" + id, nil
}

// EnsureGateway creates the trusted audience binding before it enables login.
// The controller owns this client. It never adopts an unmarked client.
func (c *Client) EnsureGateway(ctx context.Context, id, name string) (string, error) {
	clientID, err := GatewayClientID(id)
	if err != nil {
		return "", err
	}
	if name == "" || len(name) > 255 {
		return "", errors.New("invalid Gateway name")
	}
	desired := kcClient{ClientID: clientID, Name: name, Protocol: "openid-connect", PublicClient: true, StandardFlowEnabled: true,
		RedirectURIs: []string{"http://127.0.0.1:*", "http://localhost:*"}, WebOrigins: []string{},
		DefaultClientScopes: []string{}, OptionalClientScopes: []string{},
		Attributes: map[string]string{gatewayAttribute: "true", gatewayIDAttribute: id, "pkce.code.challenge.method": "S256", deviceGrantAttribute: "true", cibaGrantAttribute: "false", accessTokenLifespanAttribute: "300", "backchannel.logout.session.required": "true", "backchannel.logout.revoke.offline.tokens": "true", "realm_client": "false"},
	}
	uuid, err := c.clientUUID(ctx, clientID)
	if err != nil {
		return "", err
	}
	if uuid == "" {
		body, _ := json.Marshal(desired)
		_, code, err := c.admin(ctx, http.MethodPost, fmt.Sprintf("/admin/realms/%s/clients", c.realm), body)
		if err != nil {
			return "", err
		}
		if code != http.StatusCreated && code != http.StatusConflict {
			return "", statusError("create Gateway client", code)
		}
		uuid, err = c.clientUUID(ctx, clientID)
		if err != nil {
			return "", err
		}
		if uuid == "" {
			return "", ErrNotFound
		}
	}
	live, err := c.requireGateway(ctx, uuid, id)
	if err != nil {
		return "", err
	}
	mappersOK, err := c.protocolMappersConverged(ctx, uuid, clientID)
	if err != nil {
		return "", err
	}
	rolesOK, err := c.gatewayRoles(ctx, uuid, false)
	if err != nil {
		return "", err
	}
	scopesOK, err := c.gatewayScopes(ctx, uuid, false)
	if err != nil {
		return "", err
	}
	if !gatewayConfigurationEqual(live, &desired) || !mappersOK || !rolesOK || !scopesOK {
		// A failed configuration remains disabled. A later pass repairs it.
		if err := c.setEnabled(ctx, uuid, false); err != nil {
			return "", err
		}
		body, _ := json.Marshal(desired)
		_, code, err := c.admin(ctx, http.MethodPut, fmt.Sprintf("/admin/realms/%s/clients/%s", c.realm, url.PathEscape(uuid)), body)
		if err != nil {
			return "", err
		}
		if code != http.StatusNoContent {
			return "", statusError("configure Gateway client", code)
		}
		if _, err := c.gatewayRoles(ctx, uuid, true); err != nil {
			return "", err
		}
		if _, err := c.gatewayScopes(ctx, uuid, true); err != nil {
			return "", err
		}
		if !mappersOK {
			if err := c.replaceProtocolMappers(ctx, uuid, clientID); err != nil {
				return "", err
			}
		}
		live, err = c.requireGateway(ctx, uuid, id)
		if err != nil {
			return "", err
		}
		mappersOK, err = c.protocolMappersConverged(ctx, uuid, clientID)
		if err != nil {
			return "", err
		}
		rolesOK, err = c.gatewayRoles(ctx, uuid, false)
		if err != nil {
			return "", err
		}
		scopesOK, err = c.gatewayScopes(ctx, uuid, false)
		if err != nil {
			return "", err
		}
		if !gatewayConfigurationEqual(live, &desired) {
			return "", errors.New("Gateway client configuration did not converge")
		}
		if !mappersOK {
			return "", errors.New("Gateway claim mappers did not converge")
		}
		if !rolesOK || !scopesOK {
			return "", errors.New("Gateway role scopes did not converge")
		}
	}
	if !live.Enabled {
		if err := c.setEnabled(ctx, uuid, true); err != nil {
			return "", err
		}
		live, err = c.requireGateway(ctx, uuid, id)
		if err != nil {
			return "", err
		}
		if !live.Enabled || !gatewayConfigurationEqual(live, &desired) {
			return "", errors.New("Gateway client enablement did not converge")
		}
	}
	body, _ := json.Marshal(map[string]any{"issuer": c.issuer(), "client_id": clientID, "audience": clientID, "jwks_ttl": 3600, "roles_claim": "hypershell.roles", "admin_role": RoleAdmin, "user_role": RoleUser})
	return string(body), nil
}

func gatewayConfigurationEqual(live, desired *kcClient) bool {
	return live.ClientID == desired.ClientID && live.Name == desired.Name && live.Protocol == desired.Protocol && live.PublicClient && live.StandardFlowEnabled &&
		!live.ServiceAccountsEnabled && !live.ImplicitFlowEnabled && !live.DirectAccessGrantsEnabled && !live.AuthorizationServicesEnabled && !live.FullScopeAllowed &&
		equalStrings(live.RedirectURIs, desired.RedirectURIs) && len(live.WebOrigins) == 0 && len(live.DefaultClientScopes) == 0 && len(live.OptionalClientScopes) == 0 && reflect.DeepEqual(live.Attributes, desired.Attributes)
}
func (c *Client) requireGateway(ctx context.Context, uuid, id string) (*kcClient, error) {
	clientID, err := GatewayClientID(id)
	if err != nil {
		return nil, err
	}
	live, err := c.getClient(ctx, uuid)
	if err != nil {
		return nil, err
	}
	if live.ClientID != clientID || live.Attributes[gatewayAttribute] != "true" || live.Attributes[gatewayIDAttribute] != id {
		return nil, errors.New("Gateway client ownership does not match")
	}
	return live, nil
}
func (c *Client) gatewayRoles(ctx context.Context, uuid string, create bool) (bool, error) {
	path := fmt.Sprintf("/admin/realms/%s/clients/%s/roles", c.realm, url.PathEscape(uuid))
	body, code, err := c.admin(ctx, http.MethodGet, path, nil)
	if err != nil {
		return false, err
	}
	if code != http.StatusOK {
		return false, statusError("list Gateway roles", code)
	}
	var roles []kcRole
	if json.Unmarshal(body, &roles) != nil {
		return false, errors.New("invalid Gateway roles")
	}
	present := map[string]bool{}
	for _, role := range roles {
		present[role.Name] = true
	}
	complete := true
	for _, name := range []string{RoleUser, RoleAdmin} {
		if present[name] {
			continue
		}
		complete = false
		if !create {
			continue
		}
		payload, _ := json.Marshal(map[string]string{"name": name})
		_, code, err := c.admin(ctx, http.MethodPost, path, payload)
		if err != nil {
			return false, err
		}
		if code != http.StatusCreated && code != http.StatusConflict {
			return false, statusError("create Gateway role", code)
		}
	}
	return complete, nil
}

// DeleteGateway requires both the immutable client name and the trusted binding.
// The caller must first obtain an explicit deletion state from the API.
func (c *Client) DeleteGateway(ctx context.Context, id string) error {
	clientID, err := GatewayClientID(id)
	if err != nil {
		return err
	}
	uuid, err := c.clientUUID(ctx, clientID)
	if err != nil {
		return err
	}
	if uuid == "" {
		return nil
	}
	if _, err := c.requireGateway(ctx, uuid, id); err != nil {
		return err
	}
	if err := c.deleteClient(ctx, uuid); err != nil {
		return err
	}
	remaining, err := c.clientUUID(ctx, clientID)
	if err != nil {
		return err
	}
	if remaining != "" {
		return errors.New("Gateway identity cleanup is pending")
	}
	return nil
}

// GatewayIDs supplies a bounded inventory for recovery after an offline deletion.
func (c *Client) GatewayIDs(ctx context.Context) ([]string, error) {
	ids := []string{}
	for first := 0; first < 10000; first += 100 {
		path := fmt.Sprintf("/admin/realms/%s/clients?first=%d&max=100", c.realm, first)
		body, code, err := c.admin(ctx, http.MethodGet, path, nil)
		if err != nil {
			return nil, err
		}
		if code != http.StatusOK {
			return nil, statusError("list Gateway clients", code)
		}
		var clients []kcClient
		if json.Unmarshal(body, &clients) != nil || len(clients) > 100 {
			return nil, errors.New("invalid Gateway client inventory")
		}
		for _, client := range clients {
			if client.Attributes[gatewayAttribute] != "true" {
				continue
			}
			id := client.Attributes[gatewayIDAttribute]
			expected, err := GatewayClientID(id)
			if err == nil && client.ClientID == expected {
				ids = append(ids, id)
			}
		}
		if len(clients) < 100 {
			return ids, nil
		}
	}
	return nil, errors.New("Keycloak client inventory exceeds its limit")
}

func (c *Client) gatewayScopes(ctx context.Context, uuid string, repair bool) (bool, error) {
	body, code, err := c.admin(ctx, http.MethodGet, fmt.Sprintf("/admin/realms/%s/clients/%s/roles", c.realm, url.PathEscape(uuid)), nil)
	if err != nil {
		return false, err
	}
	if code != http.StatusOK {
		return false, statusError("read Gateway roles", code)
	}
	var available []kcRole
	if json.Unmarshal(body, &available) != nil {
		return false, errors.New("invalid Gateway roles")
	}
	roles := []kcRole{}
	for _, role := range available {
		if role.Name == RoleUser || role.Name == RoleAdmin {
			roles = append(roles, role)
		}
	}
	if len(roles) != 2 {
		return false, nil
	}
	ok, err := c.scopeMappingsConverged(ctx, uuid, uuid, roles)
	if err != nil || ok || !repair {
		return ok, err
	}
	if err := c.replaceScopeMappings(ctx, uuid, uuid, roles); err != nil {
		return false, err
	}
	return c.scopeMappingsConverged(ctx, uuid, uuid, roles)
}
