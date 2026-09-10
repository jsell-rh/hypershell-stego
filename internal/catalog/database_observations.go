package catalog

import (
	"encoding/json"

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
		return policy.AuthorizeControllerWrite(p, "ManagedDatabase", "observe.provider", row.Provider)
	}
}
