package serviceaccountkeycloak

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	provider "github.com/jsell-rh/hypershell-stego/out/keycloak"
)

// ReconcileGatewayUser changes only roles for this managed Gateway client.
// The caller supplies a stored issuer, subject, and authorized Gateway grant.
// People and API automation use the same role policy. Profile names are not IDs.
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
	gatewayUUID, err := c.clientUUID(ctx, clientID)
	if err != nil {
		return err
	}
	if gatewayUUID == "" {
		return ErrNotFound
	}
	var names []string
	if role != "" {
		names = desiredRoleNames(providerRole)
	}
	return c.keycloak.ReconcileUserClientRoles(ctx, provider.ClientBinding{
		ID: gatewayUUID, ClientID: clientID,
		Attributes: map[string]string{gatewayAttribute: "true", gatewayIDAttribute: id},
	}, subject, names)
}
