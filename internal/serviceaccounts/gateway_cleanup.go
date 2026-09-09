package serviceaccounts

import (
	"context"
	"errors"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// CleanupGateway runs after authorization while the caller holds the Gateway
// row lock. Provider removal precedes the atomic metadata, audit, and event
// commit. A failed commit can be retried with the same immutable resource IDs.
func (s *Service) CleanupGateway(ctx context.Context, tx storage.Transaction, gatewayID string) error {
	provider := s.provider
	if provider == nil {
		history, err := tx.List(ctx, "ServiceAccount", "gateway_id", gatewayID, storage.ListOptions{Page: 1, Size: 0, CountOnly: true, IncludeDeleted: true})
		if err != nil {
			return err
		}
		if history.Total != 0 {
			return gateways.ErrGatewayCleanupUnavailable
		}
		return nil
	}
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	err := provider.DeleteGateway(call, gatewayID)
	cancel()
	if err != nil {
		return gateways.ErrGatewayCleanupUnavailable
	}
	for {
		result, err := tx.List(ctx, "ServiceAccount", "gateway_id", gatewayID, storage.ListOptions{Page: 1, Size: 100})
		if err != nil {
			return err
		}
		rows, ok := result.Items.([]model.ServiceAccount)
		if !ok {
			return errors.New("unexpected Gateway account cleanup result")
		}
		for _, row := range rows {
			// Keep the tombstone in the historical cleanup scan. A remote create can
			// finish after a lost database connection released its row lock.
			row.Status = "deleting"
			row.Active = false
			row.ActiveName = nil
			row.LastError = nil
			if row.RevokedAt == nil {
				now := s.now().UTC()
				row.RevokedAt = &now
			}
			if err := save(ctx, tx, &row); err != nil {
				return err
			}
			if err := audit(ctx, tx, row, "system", "gateway_cleanup", "succeeded"); err != nil {
				return err
			}
			if err := tx.Delete(ctx, "ServiceAccount", row.ID); err != nil {
				return err
			}
		}
		if len(rows) < 100 {
			return nil
		}
	}
}
