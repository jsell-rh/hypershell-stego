package acceptance

import "github.com/jsell-rh/hypershell-stego/internal/httpapi"

// These decoders retain the prior HTTP fields independently of generated types.
type gatewayResponse struct {
	httpapi.Reference
	Name               string   `json:"name"`
	ClusterID          string   `json:"cluster_id"`
	ReleaseID          string   `json:"release_id"`
	Namespace          string   `json:"namespace"`
	ExternalDNS        *string  `json:"external_dns,omitempty"`
	TLSMode            *string  `json:"tls_mode,omitempty"`
	ServiceType        *string  `json:"service_type,omitempty"`
	Status             *string  `json:"status,omitempty"`
	Phase              *string  `json:"phase,omitempty"`
	Image              *string  `json:"image,omitempty"`
	SupervisorImage    *string  `json:"supervisor_image,omitempty"`
	ServerDNSNames     []string `json:"server_dns_names,omitempty"`
	RouteAddress       *string  `json:"route_address,omitempty"`
	ConsoleAddress     *string  `json:"console_address,omitempty"`
	OIDC               *string  `json:"oidc,omitempty"`
	Route              *string  `json:"route,omitempty"`
	CredentialDriver   *string  `json:"credential_driver,omitempty"`
	ActiveSandboxCount *int32   `json:"active_sandbox_count,omitempty"`
	CreatedBy          string   `json:"created_by,omitempty"`
}
type gatewayListResponse struct {
	Kind  string            `json:"kind"`
	Href  string            `json:"href"`
	Page  int               `json:"page"`
	Size  int               `json:"size"`
	Total int64             `json:"total"`
	Items []gatewayResponse `json:"items"`
}
