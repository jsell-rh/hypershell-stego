package serviceaccountkeycloak

import (
	"context"
	"encoding/json"
	"errors"

	provider "github.com/jsell-rh/hypershell-stego/out/keycloak"
	"github.com/segmentio/ksuid"
)

const gatewayAttribute = "hypershell.gateway"
const managedGatewayAttribute = "stego.owner.hypershell.gateway"
const managedGatewayIDAttribute = "stego.owner.hypershell.gateway-id"

// GatewayClientID uses the immutable Gateway ID. A rename does not change tokens.
func GatewayClientID(id string) (string, error) {
	parsed, err := ksuid.Parse(id)
	if err != nil || parsed == ksuid.Nil || parsed.String() != id {
		return "", errors.New("invalid Gateway ID")
	}
	return "hs-gateway-" + id, nil
}

// Gateway policy supplies ownership, role names, claims, and login callbacks.
// STEGO owns discovery, recovery records, provider changes, and cleanup.
func gatewayIdentity(id string) (provider.NativeClientIdentity, error) {
	clientID, err := GatewayClientID(id)
	if err != nil {
		return provider.NativeClientIdentity{}, err
	}
	return provider.NativeClientIdentity{ClientID: clientID,
		Ownership:        map[string]string{managedGatewayAttribute: "true", managedGatewayIDAttribute: id},
		LegacyAttributes: map[string]string{gatewayAttribute: "true", gatewayIDAttribute: id},
		LegacyRenames:    map[string]string{gatewayAttribute: managedGatewayAttribute, gatewayIDAttribute: managedGatewayIDAttribute},
	}, nil
}
func (c *Client) gatewayLifecycle(id string, revision int64, cleanup bool) (*provider.NativeClientLifecycle, error) {
	if c.gatewayJournal == nil || revision < 1 {
		return nil, errors.New("Gateway identity requires a protected journal and resource revision")
	}
	identity, err := gatewayIdentity(id)
	if err != nil {
		return nil, err
	}
	journal, err := c.gatewayJournal(id, revision, cleanup)
	if err != nil {
		return nil, err
	}
	return provider.NewNativeClientLifecycle(c.keycloak, journal, identity)
}
func (c *Client) reconcileGateway(ctx context.Context, id, name string, revision int64) (provider.ClientBinding, error) {
	if name == "" {
		return provider.ClientBinding{}, errors.New("Gateway name is required")
	}
	lifecycle, err := c.gatewayLifecycle(id, revision, false)
	if err != nil {
		return provider.ClientBinding{}, err
	}
	binding, err := lifecycle.Reconcile(ctx, func(binding provider.ClientBinding) (provider.NativeAccessPolicy, error) {
		return provider.NativeAccessPolicy{
			Client: provider.NativeClientPolicy{DisplayName: name, AccessTokenLifetimeSeconds: 300, LoopbackRedirectURIs: []string{"http://127.0.0.1:*/callback", "http://localhost:*/callback"}, EnableDeviceAuthorization: true},
			Roles:  []string{RoleAdmin, RoleUser},
			Scopes: provider.RolePolicy{Clients: []provider.ClientRoleGrant{{Client: binding, Names: []string{RoleAdmin, RoleUser}}}},
			Claims: provider.TokenClaimsPolicy{AudienceClients: []provider.ClientBinding{binding}, ClientRoles: []provider.ClientRoleClaim{{Client: binding, Claim: "hypershell.roles"}}},
		}, nil
	})
	if err != nil {
		return provider.ClientBinding{}, err
	}
	return binding, nil
}
func (c *Client) gatewayOIDC(binding provider.ClientBinding) string {
	body, _ := json.Marshal(map[string]any{"issuer": c.issuer(), "client_id": binding.ClientID, "audience": binding.ClientID, "jwks_ttl": 3600, "roles_claim": "hypershell.roles", "admin_role": RoleAdmin, "user_role": RoleUser})
	return string(body)
}
func (c *Client) EnsureGateway(ctx context.Context, id, name string, revision int64) (string, error) {
	binding, err := c.reconcileGateway(ctx, id, name, revision)
	if err != nil {
		return "", err
	}
	return c.gatewayOIDC(binding), nil
}

func (c *Client) DeleteGateway(ctx context.Context, id string, revision int64) error {
	if c.consoleJournal != nil {
		console, err := c.consoleLifecycle(id, revision, true)
		if err != nil {
			return err
		}
		if err = console.Close(ctx); err != nil {
			return err
		}
	}
	lifecycle, err := c.gatewayLifecycle(id, revision, true)
	if err != nil {
		return err
	}
	return lifecycle.Close(ctx)
}

// Reads accept a complete legacy binding until its controller migrates it.
// Mixed ownership keys are not a usable grant or service-account audience.
func gatewayBinding(live *provider.ClientRepresentation, id string) (provider.ClientBinding, error) {
	clientID, err := GatewayClientID(id)
	if err != nil {
		return provider.ClientBinding{}, err
	}
	return gatewayAudienceBinding(live, id, clientID)
}

// Stored legacy audiences can have an older public name. The trusted caller
// must supply that exact name and Gateway ID. New ownership requires the
// generated name, and both forms retain the common reserved-key checks.
func gatewayAudienceBinding(live *provider.ClientRepresentation, id, clientID string) (provider.ClientBinding, error) {
	expected, err := gatewayIdentity(id)
	if err != nil {
		return provider.ClientBinding{}, err
	}
	if live == nil || live.ClientID != clientID {
		return provider.ClientBinding{}, provider.ErrOwnership
	}
	attributes := expected.LegacyAttributes
	_, newKind := live.Attributes[managedGatewayAttribute]
	_, newID := live.Attributes[managedGatewayIDAttribute]
	if newKind || newID {
		if clientID != expected.ClientID {
			return provider.ClientBinding{}, provider.ErrOwnership
		}
		if _, ok := live.Attributes[gatewayAttribute]; ok {
			return provider.ClientBinding{}, provider.ErrOwnership
		}
		if _, ok := live.Attributes[gatewayIDAttribute]; ok {
			return provider.ClientBinding{}, provider.ErrOwnership
		}
		attributes = expected.Ownership
	}
	binding := provider.ClientBinding{ID: live.ID, ClientID: clientID, Attributes: attributes}
	if err := binding.CheckOwnership(*live); err != nil {
		return provider.ClientBinding{}, err
	}
	return binding, nil
}
func (c *Client) requireGateway(ctx context.Context, uuid, id string) (*provider.ClientRepresentation, error) {
	live, err := c.getClient(ctx, uuid)
	if err != nil {
		return nil, err
	}
	if _, err = gatewayBinding(live, id); err != nil {
		return nil, err
	}
	return live, nil
}

// GatewayIDs supplies a bounded inventory for recovery after an offline deletion.
func (c *Client) GatewayIDs(ctx context.Context) ([]string, error) {
	ids := []string{}
	seen := map[string]bool{}
	resources := map[string]bool{}
	for first := 0; first < provider.MaxInventory; first += provider.MaxPageSize {
		clients, err := c.keycloak.ListClients(ctx, provider.Page{First: first, Size: provider.MaxPageSize})
		if err != nil {
			return nil, err
		}
		for _, client := range clients {
			if seen[client.ID] {
				return nil, errors.New("Gateway client inventory repeats an ID")
			}
			seen[client.ID] = true
			id := client.Attributes[managedGatewayIDAttribute]
			if id == "" {
				id = client.Attributes[gatewayIDAttribute]
			}
			_, nativeErr := gatewayBinding(&client, id)
			_, consoleErr := consoleBinding(&client, id)
			expected, err := GatewayClientID(id)
			native := nativeErr == nil && err == nil && client.ClientID == expected
			if (native || consoleErr == nil) && !resources[id] {
				resources[id] = true
				ids = append(ids, id)
			}
		}
		if len(clients) < provider.MaxPageSize {
			return ids, nil
		}
	}
	return nil, errors.New("Keycloak client inventory exceeds its limit")
}
