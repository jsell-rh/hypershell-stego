package serviceaccountkeycloak

import (
	"context"
	"errors"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	provider "github.com/jsell-rh/hypershell-stego/out/keycloak"
	"github.com/segmentio/ksuid"
)

func gatewayInventoryQuery(id string) (string, error) {
	value, err := ksuid.Parse(id)
	if err != nil || value == ksuid.Nil || value.String() != id {
		return "", ErrNotManaged
	}
	return "hs-sa-" + id + "-", nil
}
func (c *Client) GatewayInventorySource(gatewayID string) (string, error) {
	query, err := gatewayInventoryQuery(gatewayID)
	if err != nil {
		return "", err
	}
	return c.keycloak.ClientNameSourceVersion(query)
}
func (c *Client) checkInventorySource(gatewayID, version string) (string, error) {
	current, err := c.GatewayInventorySource(gatewayID)
	if err != nil {
		return "", err
	}
	if version != current {
		return "", runtime.ErrScanContract
	}
	return gatewayInventoryQuery(gatewayID)
}
func (c *Client) GatewayInventoryPage(ctx context.Context, gatewayID, version, after string, limit int) (runtime.CursorPage[string], error) {
	result := runtime.CursorPage[string]{}
	query, err := c.checkInventorySource(gatewayID, version)
	if err != nil {
		return result, err
	}
	source, err := c.keycloak.ClientNameCursorSource(query)
	if err != nil {
		return result, err
	}
	page, err := source(ctx, after, limit)
	if err != nil {
		return result, err
	}
	for _, item := range page.Items {
		result.Items = append(result.Items, runtime.CursorItem[string]{Cursor: item.Cursor, Value: item.Value.ID})
	}
	result.More = page.More
	return result, nil
}

// PrepareGatewayInventoryCandidate saves cleanup intent after current ownership
// checks. It does not disable or delete the provider client.
func (c *Client) PrepareGatewayInventoryCandidate(ctx context.Context, gatewayID, version, providerID string) (bool, error) {
	if _, err := c.checkInventorySource(gatewayID, version); err != nil {
		return false, err
	}
	value, err := c.getClient(ctx, providerID)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	_, accountID, owned, err := managedInventoryAccount(value, gatewayID)
	if err != nil || !owned {
		return false, err
	}
	lifecycle, err := c.accountLifecycle(gatewayID, accountID, true)
	if err != nil {
		return false, err
	}
	if err = lifecycle.PrepareCloseExisting(ctx, providerID); err != nil {
		return false, err
	}
	return true, nil
}
func managedInventoryAccount(client *provider.ClientRepresentation, gatewayID string) (string, string, bool, error) {
	parent, account := client.Attributes[gatewayIDAttribute], client.Attributes[serviceAccountIDAttribute]
	if client.Attributes["stego.owner."+managedAttribute] == "true" {
		parent = client.Attributes["stego.owner."+gatewayIDAttribute]
		account = client.Attributes["stego.owner."+serviceAccountIDAttribute]
	}
	if client.Attributes[managedAttribute] != "true" && client.Attributes["stego.owner."+managedAttribute] != "true" {
		return "", "", false, nil
	}
	if gatewayID != "" && parent != gatewayID {
		return "", "", false, nil
	}
	if _, err := accountBinding(client, parent, account); err != nil {
		return "", "", false, err
	}
	return parent, account, true, nil
}
