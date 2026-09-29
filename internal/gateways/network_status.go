package gateways

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"

	"github.com/jsell-rh/hypershell-stego/internal/resourceevents"
)

// ObserveNetworkStatus commits a controller's deterministic reconciliation
// verdict for one GatewayNetwork. The caller must have read the resource
// revision before its observation; a stale save fails so the controller can
// retry from current state. An unchanged status writes no row and emits no
// event.
func (s *Service) ObserveNetworkStatus(ctx context.Context, p Principal, id string, version int64, status string) error {
	if err := validatePrincipal(p); err != nil {
		return err
	}
	if !s.isControlPlane(p) {
		return ErrForbidden
	}
	if err := s.AuthorizeControllerWrite(p, "GatewayNetwork", "observe.network", ""); err != nil {
		return err
	}
	if !validID(id) {
		return store.ErrNotFound
	}
	if strings.TrimSpace(status) == "" || len(status) > 255 || !utf8.ValidString(status) || strings.ContainsRune(status, 0) {
		return ErrInvalid
	}
	if version < 1 {
		return ErrObservationRequired
	}
	return s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		value, err := tx.Get(ctx, "GatewayNetwork", id)
		if err != nil {
			return err
		}
		row, ok := value.(model.GatewayNetwork)
		if !ok {
			return errors.New("unexpected GatewayNetwork storage result")
		}
		// The revision read before the observation must still own the row.
		if row.UpdatedTime.Unix() != version {
			return store.ErrVersionConflict
		}
		if row.Status != nil && *row.Status == status {
			return nil
		}
		row.Status = &status
		if err := tx.Replace(ctx, "GatewayNetwork", id, row); err != nil {
			return err
		}
		return resourceevents.Notify(tx, "GatewayNetworks", id, "Update", "gatewaynetwork.updated")
	})
}
