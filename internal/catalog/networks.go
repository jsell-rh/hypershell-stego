package catalog

import (
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// NetworkCreate describes a network record. These fields do not configure tunnels.
type NetworkCreate struct {
	Name         string  `json:"name,omitempty"`
	Topology     *string `json:"topology,omitempty"`
	TunnelMode   *string `json:"tunnel_mode,omitempty"`
	HubGatewayID *string `json:"hub_gateway_id,omitempty"`
	Status       *string `json:"status,omitempty"`
}

type NetworkPatch struct {
	Name         *string `json:"name,omitempty"`
	Topology     *string `json:"topology,omitempty"`
	TunnelMode   *string `json:"tunnel_mode,omitempty"`
	HubGatewayID *string `json:"hub_gateway_id,omitempty"`
	Status       *string `json:"status,omitempty"`
}

func newNetwork(id string, input NetworkCreate) (model.GatewayNetwork, error) {
	row := model.GatewayNetwork{Meta: model.Meta{ID: id}, Name: input.Name, Topology: input.Topology, TunnelMode: input.TunnelMode, HubGatewayID: input.HubGatewayID, Status: input.Status}
	return row, validateNetwork(row)
}

func patchNetwork(row *model.GatewayNetwork, input NetworkPatch) error {
	if input.Name != nil {
		row.Name = *input.Name
	}
	if input.Topology != nil {
		row.Topology = input.Topology
	}
	if input.TunnelMode != nil {
		row.TunnelMode = input.TunnelMode
	}
	if input.HubGatewayID != nil {
		row.HubGatewayID = input.HubGatewayID
	}
	if input.Status != nil {
		row.Status = input.Status
	}
	return validateNetwork(*row)
}

func validateNetwork(row model.GatewayNetwork) error {
	if !textField(row.Name, true, 255) {
		return gateways.ErrInvalid
	}
	for _, value := range []*string{row.Topology, row.TunnelMode} {
		if value != nil && !textField(*value, false, 64) {
			return gateways.ErrInvalid
		}
	}
	for _, value := range []*string{row.HubGatewayID, row.Status} {
		if value != nil && !textField(*value, false, 255) {
			return gateways.ErrInvalid
		}
	}
	return nil
}
