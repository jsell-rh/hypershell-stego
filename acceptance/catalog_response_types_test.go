package acceptance

import "github.com/jsell-rh/hypershell-stego/internal/httpapi"

// These types check the HTTP contract independently of generated response types.
type catalogManagedCluster struct {
	httpapi.Reference
	Name             string  `json:"name"`
	Provider         string  `json:"provider"`
	Region           *string `json:"region,omitempty"`
	KubeconfigSecret string  `json:"kubeconfig_secret"`
	Status           *string `json:"status,omitempty"`
	ApiServerUrl     *string `json:"api_server_url,omitempty"`
}

type catalogGatewayRelease struct {
	httpapi.Reference
	Name            string  `json:"name"`
	Image           string  `json:"image"`
	RolloutStrategy *string `json:"rollout_strategy,omitempty"`
	CanaryPercent   *int32  `json:"canary_percent,omitempty"`
	CanaryDuration  *string `json:"canary_duration,omitempty"`
	Status          *string `json:"status,omitempty"`
}

type catalogGatewayNetwork struct {
	httpapi.Reference
	Name         string  `json:"name"`
	Topology     *string `json:"topology,omitempty"`
	TunnelMode   *string `json:"tunnel_mode,omitempty"`
	HubGatewayID *string `json:"hub_gateway_id,omitempty"`
	Status       *string `json:"status,omitempty"`
}
