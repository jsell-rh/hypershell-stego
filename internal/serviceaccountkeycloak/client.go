package serviceaccountkeycloak

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	provider "github.com/jsell-rh/hypershell-stego/out/keycloak"
	"github.com/segmentio/ksuid"
)

const (
	RoleUser                       = "openshell-user"
	RoleAdmin                      = "openshell-admin"
	managedAttribute               = "hypershell.service-account"
	gatewayIDAttribute             = "hypershell.gateway-id"
	serviceAccountIDAttribute      = "hypershell.service-account-id"
	creatorUserIDAttribute         = "hypershell.creator-user-id"
	accessTokenLifespanAttribute   = "access.token.lifespan"
	clientRefreshTokenAttribute    = "client_credentials.use_refresh_token"
	deviceGrantAttribute           = "oauth2.device.authorization.grant.enabled"
	cibaGrantAttribute             = "oidc.ciba.grant.enabled"
	builtInServiceAccountScope     = "service_account"
	defaultAccessTokenLifetimeSecs = 300
)

var ErrNotFound = provider.ErrNotFound

var ErrNotManaged = provider.ErrOwnership

type ServiceAccountSpec struct {
	ClientID                   string
	DisplayName                string
	GatewayClientID            string
	GatewayID                  string
	ServiceAccountID           string
	CreatorUserID              string
	Role                       string
	ExpectedIssuer             string
	AccessTokenLifetimeSeconds int
}

type ProvisionedServiceAccount struct {
	ClientUUID   string
	ClientID     string
	ClientSecret string
	Subject      string
}

func (ProvisionedServiceAccount) String() string {
	return "ProvisionedServiceAccount{credential redacted}"
}
func (p ProvisionedServiceAccount) GoString() string           { return p.String() }
func (p ProvisionedServiceAccount) Format(s fmt.State, _ rune) { _, _ = s.Write([]byte(p.String())) }
func (ProvisionedServiceAccount) MarshalJSON() ([]byte, error) {
	return nil, errors.New("credential serialization requires an explicit response")
}

type ManagedClient struct {
	UUID             string
	ClientID         string
	GatewayID        string
	ServiceAccountID string
}

type Client struct {
	serverURL      string
	realm          string
	clientID       string
	secretFile     string
	keycloak       *provider.Client
	gatewayJournal func(string, int64, bool) (*runtime.StateJournal, error)
	accountJournal func(string, string, bool) (*runtime.StateJournal, error)
}

type Options struct {
	ServerURL, Realm, ClientID, SecretFile, CAFile string
	GatewayJournal                                 func(string, int64, bool) (*runtime.StateJournal, error)
	AccountJournal                                 func(string, string, bool) (*runtime.StateJournal, error)
}

func (c *Client) String() string             { return "KeycloakClient{credentials redacted}" }
func (c *Client) GoString() string           { return c.String() }
func (c *Client) Format(s fmt.State, _ rune) { _, _ = s.Write([]byte(c.String())) }
func (c *Client) MarshalJSON() ([]byte, error) {
	return nil, errors.New("Keycloak client cannot be serialized")
}

func NewClient(options Options) (*Client, error) {
	common, err := provider.New(provider.Options{ServerURL: options.ServerURL, Realm: options.Realm, ClientID: options.ClientID, SecretFile: options.SecretFile, CAFile: options.CAFile})
	if err != nil {
		return nil, err
	}
	return &Client{gatewayJournal: options.GatewayJournal, accountJournal: options.AccountJournal, keycloak: common, serverURL: options.ServerURL, realm: options.Realm, clientID: options.ClientID, secretFile: options.SecretFile}, nil
}
func (c *Client) Close() {
	if c != nil {
		c.keycloak.Close()
	}
}

func (c *Client) Configured() bool {
	return c != nil && c.serverURL != "" && c.realm != "" && c.clientID != "" && c.secretFile != ""
}

func (c *Client) issuer() string { return c.keycloak.Issuer() }

func accountIdentity(gatewayID, accountID string) (provider.ClientIdentity, error) {
	for _, id := range []string{gatewayID, accountID} {
		value, err := ksuid.Parse(id)
		if err != nil || value == ksuid.Nil || value.String() != id {
			return provider.ClientIdentity{}, ErrNotManaged
		}
	}
	legacy := map[string]string{managedAttribute: "true", gatewayIDAttribute: gatewayID, serviceAccountIDAttribute: accountID}
	ownership, renames := map[string]string{}, map[string]string{}
	for key, value := range legacy {
		next := "stego.owner." + key
		ownership[next] = value
		renames[key] = next
	}
	return provider.ClientIdentity{ClientID: "hs-sa-" + gatewayID + "-" + accountID, Ownership: ownership, LegacyAttributes: legacy, LegacyRenames: renames}, nil
}
func (c *Client) accountLifecycle(gatewayID, accountID string, cleanup bool) (*provider.ServiceAccountClientLifecycle, error) {
	identity, err := accountIdentity(gatewayID, accountID)
	if err != nil {
		return nil, err
	}
	if c.accountJournal == nil {
		return nil, errors.New("service-account provider journal is required")
	}
	journal, err := c.accountJournal(gatewayID, accountID, cleanup)
	if err != nil {
		return nil, err
	}
	return provider.NewServiceAccountClientLifecycle(c.keycloak, journal, identity)
}
func (c *Client) accountPolicy(ctx context.Context, spec ServiceAccountSpec) (provider.ServiceAccountLifecyclePolicy, error) {
	uuid, err := c.clientUUID(ctx, spec.GatewayClientID)
	if err != nil {
		return provider.ServiceAccountLifecyclePolicy{}, err
	}
	if uuid == "" {
		return provider.ServiceAccountLifecyclePolicy{}, ErrNotFound
	}
	roles, err := c.serviceAccountRoles(ctx, spec, uuid)
	if err != nil {
		return provider.ServiceAccountLifecyclePolicy{}, err
	}
	binding := roles.Clients[0].Client
	lifetime := spec.AccessTokenLifetimeSeconds
	if lifetime == 0 {
		lifetime = defaultAccessTokenLifetimeSecs
	}
	return provider.ServiceAccountLifecyclePolicy{Client: provider.ServiceAccountPolicy{DisplayName: spec.DisplayName, AccessTokenLifetimeSeconds: lifetime}, Roles: roles, Scopes: roles,
		Claims: provider.TokenClaimsPolicy{AudienceClients: []provider.ClientBinding{binding}, ClientRoles: []provider.ClientRoleClaim{{Client: binding, Claim: "hypershell.roles"}}, ClientMetadata: true}}, nil
}

func (c *Client) ProvisionServiceAccount(ctx context.Context, spec ServiceAccountSpec) (_ *ProvisionedServiceAccount, err error) {
	if !c.Configured() {
		return nil, errors.New("Keycloak service-account provider is not configured")
	}
	if err = c.validateSpec(spec); err != nil {
		return nil, err
	}
	desired, err := c.accountPolicy(ctx, spec)
	if err != nil {
		return nil, err
	}
	lifecycle, err := c.accountLifecycle(spec.GatewayID, spec.ServiceAccountID, false)
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if complete {
			return
		}
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		closed, failure := c.accountLifecycle(spec.GatewayID, spec.ServiceAccountID, true)
		if failure == nil {
			failure = closed.Close(cleanup)
		}
		if failure != nil {
			err = errors.Join(err, failure)
		}
	}()
	identity, err := lifecycle.Reconcile(ctx, func(provider.ClientBinding) (provider.ServiceAccountLifecyclePolicy, error) {
		return desired, nil
	})
	if err != nil {
		return nil, err
	}
	secret, err := c.keycloak.VerifiedServiceAccountSecret(ctx, identity.Client, serviceAccountTokenPolicy(spec, identity.Subject))
	if err != nil {
		return nil, err
	}
	complete = true
	return &ProvisionedServiceAccount{ClientUUID: identity.Client.ID, ClientID: identity.Client.ClientID, Subject: identity.Subject, ClientSecret: secret.Reveal()}, nil
}
func (c *Client) ReconcileServiceAccount(ctx context.Context, spec ServiceAccountSpec, uuid, subject string, enabled bool) error {
	if !c.Configured() {
		return errors.New("Keycloak service-account provider is not configured")
	}
	if err := c.validateSpec(spec); err != nil {
		return err
	}
	if uuid == "" || subject == "" {
		return ErrNotManaged
	}
	lifecycle, err := c.accountLifecycle(spec.GatewayID, spec.ServiceAccountID, false)
	if err != nil {
		return err
	}
	_, err = lifecycle.Reconcile(ctx, func(provider.ClientBinding) (provider.ServiceAccountLifecyclePolicy, error) {
		policy, err := c.accountPolicy(ctx, spec)
		policy.ExpectedProviderID = uuid
		policy.ExpectedSubject = subject
		policy.Disabled = !enabled
		return policy, err
	})
	return err
}

func accountBinding(client *provider.ClientRepresentation, gatewayID, accountID string) (provider.ClientBinding, error) {
	identity, err := accountIdentity(gatewayID, accountID)
	if err != nil {
		return provider.ClientBinding{}, err
	}
	if client == nil || client.ClientID != identity.ClientID {
		return provider.ClientBinding{}, ErrNotManaged
	}
	attrs := identity.LegacyAttributes
	current := false
	for key := range identity.Ownership {
		if _, ok := client.Attributes[key]; ok {
			current = true
		}
	}
	if current {
		for key := range identity.LegacyAttributes {
			if _, ok := client.Attributes[key]; ok {
				return provider.ClientBinding{}, ErrNotManaged
			}
		}
		attrs = identity.Ownership
	}
	binding := provider.ClientBinding{ID: client.ID, ClientID: identity.ClientID, Attributes: attrs}
	if err = binding.CheckOwnership(*client); err != nil {
		return provider.ClientBinding{}, err
	}
	return binding, nil
}
func (c *Client) requireManagedClient(ctx context.Context, uuid, gatewayID, accountID string) (*provider.ClientRepresentation, error) {
	if _, err := accountIdentity(gatewayID, accountID); err != nil {
		return nil, err
	}
	value, err := c.getClient(ctx, uuid)
	if err != nil {
		return nil, err
	}
	if _, err = accountBinding(value, gatewayID, accountID); err != nil {
		return nil, err
	}
	return value, nil
}
func (c *Client) DisableServiceAccount(ctx context.Context, uuid, gatewayID, accountID string) error {
	value, err := c.requireManagedClient(ctx, uuid, gatewayID, accountID)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	binding, err := accountBinding(value, gatewayID, accountID)
	if err != nil {
		return err
	}
	return c.keycloak.DisableClient(ctx, binding)
}
func (c *Client) DeleteServiceAccount(ctx context.Context, uuid, gatewayID, accountID string) error {
	lifecycle, err := c.accountLifecycle(gatewayID, accountID, true)
	if err != nil {
		return err
	}
	return lifecycle.CloseExisting(ctx, uuid)
}
func (c *Client) DeleteManagedServiceAccount(ctx context.Context, gatewayID, accountID string) error {
	lifecycle, err := c.accountLifecycle(gatewayID, accountID, true)
	if err != nil {
		return err
	}
	return lifecycle.Close(ctx)
}

// DeleteGatewayServiceAccounts cleans the current provider inventory. A caller
// must also recover retained account rows and journal IDs before it reports
// complete Gateway cleanup. A provider list can omit a known client.
func (c *Client) DeleteGatewayServiceAccounts(ctx context.Context, gatewayID string) error {
	if value, err := ksuid.Parse(gatewayID); err != nil || value == ksuid.Nil || value.String() != gatewayID {
		return ErrNotManaged
	}
	clients, err := c.ListManagedClients(ctx, gatewayID)
	if err != nil {
		return err
	}
	for _, client := range clients {
		if err = c.DisableServiceAccount(ctx, client.UUID, gatewayID, client.ServiceAccountID); err != nil {
			return err
		}
	}
	for _, client := range clients {
		if err = c.DeleteServiceAccount(ctx, client.UUID, gatewayID, client.ServiceAccountID); err != nil {
			return err
		}
	}
	return nil
}
func (c *Client) ListManagedClients(ctx context.Context, gatewayID string) ([]ManagedClient, error) {
	if gatewayID != "" {
		if value, err := ksuid.Parse(gatewayID); err != nil || value == ksuid.Nil || value.String() != gatewayID {
			return nil, ErrNotManaged
		}
	}
	result := []ManagedClient{}
	seen := map[string]bool{}
	for first := 0; first < provider.MaxInventory; first += provider.MaxPageSize {
		bounds := provider.Page{First: first, Size: provider.MaxPageSize}
		var page []provider.ClientRepresentation
		var err error
		if gatewayID == "" {
			page, err = c.keycloak.ListClients(ctx, bounds)
		} else {
			// The canonical name narrows discovery. Each candidate still requires its
			// current representation and exact ownership checks before use.
			page, err = c.keycloak.SearchClients(ctx, "hs-sa-"+gatewayID+"-", bounds)
		}
		if err != nil {
			return nil, err
		}
		for _, listed := range page {
			if seen[listed.ID] {
				return nil, errors.New("managed client inventory repeats an ID")
			}
			seen[listed.ID] = true
			client, err := c.getClient(ctx, listed.ID)
			if err != nil {
				return nil, err
			}
			parent, account := client.Attributes[gatewayIDAttribute], client.Attributes[serviceAccountIDAttribute]
			if client.Attributes["stego.owner."+managedAttribute] == "true" {
				parent = client.Attributes["stego.owner."+gatewayIDAttribute]
				account = client.Attributes["stego.owner."+serviceAccountIDAttribute]
			}
			if client.Attributes[managedAttribute] != "true" && client.Attributes["stego.owner."+managedAttribute] != "true" {
				continue
			}
			if gatewayID != "" && parent != gatewayID {
				continue
			}
			if _, err = accountBinding(client, parent, account); err != nil {
				return nil, err
			}
			result = append(result, ManagedClient{UUID: client.ID, ClientID: client.ClientID, GatewayID: parent, ServiceAccountID: account})
		}
		if len(page) < provider.MaxPageSize {
			return result, nil
		}
	}
	return nil, errors.New("managed client scan exceeds limit")
}

func (c *Client) validateSpec(spec ServiceAccountSpec) error {
	if spec.ExpectedIssuer != c.issuer() || spec.ClientID != "hs-sa-"+spec.GatewayID+"-"+spec.ServiceAccountID {
		return errors.New("service-account identity does not match its issuer or resource IDs")
	}
	for _, value := range []string{spec.GatewayID, spec.ServiceAccountID, spec.CreatorUserID} {
		if !regexp.MustCompile(`^[0-9A-Za-z]{27}$`).MatchString(value) {
			return errors.New("invalid resource identity")
		}
	}
	if len(spec.DisplayName) > 255 || len(spec.GatewayClientID) > 255 {
		return errors.New("service-account field exceeds limit")
	}
	if spec.ClientID == "" || spec.GatewayClientID == "" || spec.GatewayID == "" || spec.ServiceAccountID == "" || spec.CreatorUserID == "" || spec.ExpectedIssuer == "" {
		return errors.New("incomplete service-account specification")
	}
	if spec.Role != RoleUser && spec.Role != RoleAdmin {
		return errors.New("unsupported service-account role")
	}
	if spec.AccessTokenLifetimeSeconds == 0 {
		spec.AccessTokenLifetimeSeconds = defaultAccessTokenLifetimeSecs
	}
	if spec.AccessTokenLifetimeSeconds < 1 || spec.AccessTokenLifetimeSeconds > 900 {
		return errors.New("access-token lifetime must be between 1 and 900 seconds")
	}
	return nil
}

func desiredRoleNames(role string) []string {
	if role == RoleAdmin {
		return []string{RoleAdmin, RoleUser}
	}
	return []string{RoleUser}
}

func (c *Client) getClient(ctx context.Context, uuid string) (*provider.ClientRepresentation, error) {
	value, err := c.keycloak.GetClient(ctx, uuid)
	if err != nil {
		return nil, err
	}
	return &value, nil
}

func (c *Client) clientUUID(ctx context.Context, clientID string) (string, error) {
	value, err := c.keycloak.FindClient(ctx, clientID)
	if errors.Is(err, provider.ErrNotFound) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return value.ID, nil
}

func (c *Client) serviceAccountRoles(ctx context.Context, spec ServiceAccountSpec, gatewayUUID string) (provider.RolePolicy, error) {
	live, err := c.getClient(ctx, gatewayUUID)
	if err != nil {
		return provider.RolePolicy{}, err
	}
	binding, err := gatewayAudienceBinding(live, spec.GatewayID, spec.GatewayClientID)
	if err != nil || binding.ClientID != spec.GatewayClientID {
		return provider.RolePolicy{}, provider.ErrOwnership
	}
	return provider.RolePolicy{Clients: []provider.ClientRoleGrant{{Client: binding, Names: desiredRoleNames(spec.Role)}}}, nil
}

func serviceAccountTokenPolicy(spec ServiceAccountSpec, subject string) provider.ServiceAccountTokenPolicy {
	lifetime := spec.AccessTokenLifetimeSeconds
	if lifetime == 0 {
		lifetime = defaultAccessTokenLifetimeSecs
	}
	return provider.ServiceAccountTokenPolicy{Subject: subject, AccessTokenLifetimeSeconds: lifetime, Audiences: []string{spec.GatewayClientID}, RoleClaims: map[string][]string{"hypershell.roles": desiredRoleNames(spec.Role)}}
}
