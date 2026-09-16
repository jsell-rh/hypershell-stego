package serviceaccountkeycloak

type kcClient struct {
	ID                           string            `json:"id,omitempty"`
	ClientID                     string            `json:"clientId"`
	Name                         string            `json:"name,omitempty"`
	Protocol                     string            `json:"protocol,omitempty"`
	ClientAuthenticatorType      string            `json:"clientAuthenticatorType,omitempty"`
	Enabled                      bool              `json:"enabled"`
	PublicClient                 bool              `json:"publicClient"`
	ServiceAccountsEnabled       bool              `json:"serviceAccountsEnabled"`
	StandardFlowEnabled          bool              `json:"standardFlowEnabled"`
	ImplicitFlowEnabled          bool              `json:"implicitFlowEnabled"`
	DirectAccessGrantsEnabled    bool              `json:"directAccessGrantsEnabled"`
	AuthorizationServicesEnabled bool              `json:"authorizationServicesEnabled"`
	FullScopeAllowed             bool              `json:"fullScopeAllowed"`
	RedirectURIs                 []string          `json:"redirectUris"`
	WebOrigins                   []string          `json:"webOrigins"`
	DefaultClientScopes          []string          `json:"defaultClientScopes"`
	OptionalClientScopes         []string          `json:"optionalClientScopes"`
	Attributes                   map[string]string `json:"attributes,omitempty"`
}

type kcRole struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type kcUser struct {
	ID string `json:"id"`
}
