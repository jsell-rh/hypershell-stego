package serviceaccountkeycloak

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net"
	"regexp"
	"strings"

	provider "github.com/jsell-rh/hypershell-stego/out/keycloak"
	"github.com/segmentio/ksuid"
)

const managedConsoleAttribute = "stego.owner.hypershell.console"

var consoleDomainLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// GatewayConsoleOrigin selects an immutable host inside the operator's domain.
// Full ID bytes keep the host independent of names and namespace abbreviations.
func GatewayConsoleOrigin(id, domain string) (string, error) {
	parsed, err := ksuid.Parse(id)
	if err != nil || parsed == ksuid.Nil || parsed.String() != id || len(domain) > 197 || !strings.Contains(domain, ".") || net.ParseIP(domain) != nil {
		return "", errors.New("console identity requires a Gateway ID and DNS domain")
	}
	for _, label := range strings.Split(domain, ".") {
		if !consoleDomainLabel.MatchString(label) {
			return "", errors.New("console domain is invalid")
		}
	}
	return "https://console-" + hex.EncodeToString(parsed.Bytes()) + "." + domain, nil
}

func checkedConsoleDomains(input map[string]string) (map[string]string, error) {
	if len(input) > 128 {
		return nil, errors.New("too many console cluster domains")
	}
	result := make(map[string]string, len(input))
	for cluster, domain := range input {
		if _, err := GatewayConsoleOrigin(cluster, domain); err != nil {
			return nil, err
		}
		result[cluster] = domain
	}
	return result, nil
}

// ParseConsoleDomains reads the operator's cluster-to-domain policy. Empty
// configuration keeps console creation disabled during staged adoption.
func ParseConsoleDomains(raw string) (map[string]string, error) {
	invalid := errors.New("console domains require a bounded JSON object with unique cluster IDs")
	if raw == "" {
		return nil, nil
	}
	if len(raw) > 32768 {
		return nil, invalid
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	first, err := decoder.Token()
	if err != nil || first != json.Delim('{') {
		return nil, invalid
	}
	domains := map[string]string{}
	for decoder.More() {
		token, err := decoder.Token()
		cluster, ok := token.(string)
		if err != nil || !ok || len(domains) >= 128 {
			return nil, invalid
		}
		if _, exists := domains[cluster]; exists {
			return nil, invalid
		}
		var domain string
		if decoder.Decode(&domain) != nil {
			return nil, invalid
		}
		domains[cluster] = domain
	}
	last, err := decoder.Token()
	if err != nil || last != json.Delim('}') {
		return nil, invalid
	}
	if decoder.Decode(new(any)) != io.EOF {
		return nil, invalid
	}
	return checkedConsoleDomains(domains)
}

func consoleIdentity(id string) (provider.ClientIdentity, error) {
	if _, err := GatewayClientID(id); err != nil {
		return provider.ClientIdentity{}, err
	}
	return provider.ClientIdentity{ClientID: "hs-console-" + id, Ownership: map[string]string{managedConsoleAttribute: "true", managedGatewayIDAttribute: id}}, nil
}
func consoleBinding(live *provider.ClientRepresentation, id string) (provider.ClientBinding, error) {
	identity, err := consoleIdentity(id)
	if err != nil || live == nil {
		return provider.ClientBinding{}, provider.ErrOwnership
	}
	binding := provider.ClientBinding{ID: live.ID, ClientID: identity.ClientID, Attributes: identity.Ownership}
	if err = binding.CheckOwnership(*live); err != nil {
		return provider.ClientBinding{}, err
	}
	return binding, nil
}
func (c *Client) consoleLifecycle(id string, revision int64, cleanup bool) (*provider.BrowserClientLifecycle, error) {
	if c.consoleJournal == nil || revision < 1 {
		return nil, errors.New("console identity requires its protected journal and resource revision")
	}
	identity, err := consoleIdentity(id)
	if err != nil {
		return nil, err
	}
	journal, err := c.consoleJournal(id, revision, cleanup)
	if err != nil {
		return nil, err
	}
	return provider.NewBrowserClientLifecycle(c.keycloak, journal, identity)
}

// EnsureGatewayWithConsole uses the native client's roles and audience. It
// creates no duplicate user grants and returns no browser credential to the API.
func (c *Client) EnsureGatewayWithConsole(ctx context.Context, id, name, cluster string, revision int64) (string, error) {
	origin := ""
	if len(c.consoleDomains) != 0 {
		domain, ok := c.consoleDomains[cluster]
		if !ok {
			return "", errors.New("Gateway cluster has no console domain")
		}
		var err error
		origin, err = GatewayConsoleOrigin(id, domain)
		if err != nil {
			return "", err
		}
	}
	native, err := c.reconcileGateway(ctx, id, name, revision)
	if err != nil {
		return "", err
	}
	if origin != "" {
		lifecycle, err := c.consoleLifecycle(id, revision, false)
		if err != nil {
			return "", err
		}
		_, err = lifecycle.Reconcile(ctx, func(provider.ClientBinding) (provider.BrowserAccessPolicy, error) {
			return consoleAccessPolicy(native, name, origin), nil
		})
		if err != nil {
			return "", err
		}
	}
	return c.gatewayOIDC(native), nil
}

func consoleAccessPolicy(native provider.ClientBinding, name, origin string) provider.BrowserAccessPolicy {
	return provider.BrowserAccessPolicy{
		Client: provider.BrowserClientPolicy{DisplayName: name, AccessTokenLifetimeSeconds: 300, RedirectURI: origin + "/auth/callback", PostLogoutRedirectURI: origin + "/auth/logout"},
		Scopes: provider.RolePolicy{Clients: []provider.ClientRoleGrant{{Client: native, Names: []string{RoleAdmin, RoleUser}}}},
		Claims: provider.TokenClaimsPolicy{AudienceClients: []provider.ClientBinding{native}, ClientRoles: []provider.ClientRoleClaim{{Client: native, Claim: "hypershell.roles"}}},
	}
}

// GatewayConsoleCredentials reads only an open, fully reconciled client. The
// caller must check worker access and the Gateway observation before and after
// this call. This method does not create, repair, or reopen provider state.
func (c *Client) GatewayConsoleCredentials(ctx context.Context, id, name, cluster string, revision int64) (string, provider.Secret, error) {
	domain, ok := c.consoleDomains[cluster]
	if !ok || name == "" {
		return "", provider.Secret{}, errors.New("Gateway console has no current placement")
	}
	origin, err := GatewayConsoleOrigin(id, domain)
	if err != nil {
		return "", provider.Secret{}, err
	}
	lifecycle, err := c.consoleLifecycle(id, revision, false)
	if err != nil {
		return "", provider.Secret{}, err
	}
	nativeID, err := GatewayClientID(id)
	if err != nil {
		return "", provider.Secret{}, err
	}
	live, err := c.keycloak.FindClient(ctx, nativeID)
	if err != nil {
		return "", provider.Secret{}, err
	}
	native, err := gatewayBinding(&live, id)
	if err != nil {
		return "", provider.Secret{}, err
	}
	policy := gatewayAccessPolicy(native, name)
	if err := c.keycloak.InspectNativeClientAccess(ctx, native, policy); err != nil {
		return "", provider.Secret{}, err
	}
	binding, secret, err := lifecycle.Credentials(ctx, func(provider.ClientBinding) (provider.BrowserAccessPolicy, error) {
		return consoleAccessPolicy(native, name, origin), nil
	})
	if err != nil {
		return "", provider.Secret{}, err
	}
	if err := c.keycloak.InspectNativeClientAccess(ctx, native, policy); err != nil {
		return "", provider.Secret{}, err
	}
	return binding.ClientID, secret, nil
}
