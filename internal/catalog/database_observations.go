package catalog

import (
	"encoding/json"
	"github.com/jsell-rh/hypershell-stego/internal/databaseplacement"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// The transaction supplies the stored provider before it changes any field.
func databaseObservationPolicy(policy *gateways.Service) func(gateways.Principal, model.ManagedDatabase, DatabasePatch) error {
	return func(p gateways.Principal, row model.ManagedDatabase, patch DatabasePatch) error {
		if patch.Status == nil && patch.ConnectionSecret == nil {
			return gateways.ErrForbidden
		}
		rest := patch
		rest.Status, rest.ConnectionSecret = nil, nil
		data, err := json.Marshal(rest)
		if err != nil || string(data) != "{}" {
			return gateways.ErrInvalid
		}
		target, err := databaseTarget(row)
		if err != nil {
			return err
		}
		return policy.AuthorizeControllerWrite(p, "ManagedDatabase", "observe.provider", target)
	}
}

func databaseTarget(row model.ManagedDatabase) (string, error) {
	cluster := ""
	if row.ClusterID != nil {
		cluster = *row.ClusterID
	}
	target, err := databaseplacement.Target(row.Provider, cluster)
	if err != nil {
		return "", gateways.ErrForbidden
	}
	return target, nil
}
