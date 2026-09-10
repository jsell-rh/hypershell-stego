package gateways

import (
	"context"
	"errors"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

const identityScanScope = "identity-users"

func (s *Service) authorizeIdentityCheckpoint(p Principal, id string) error {
	if err := s.checkIdentityReader(p, id); err != nil {
		return err
	}
	return s.AuthorizeControllerWrite(p, "Gateway", "configure.identity", "")
}

// IdentityCheckpoint reads one cursor after the identity controller grant check.
func (s *Service) IdentityCheckpoint(ctx context.Context, p Principal, id string) (store.ScanCheckpoint, error) {
	var result store.ScanCheckpoint
	if err := s.authorizeIdentityCheckpoint(p, id); err != nil {
		return result, err
	}
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		value, err := tx.Get(ctx, "Gateway", id)
		if err != nil {
			return err
		}
		row, ok := value.(model.Gateway)
		if !ok || row.ID != id {
			return errors.New("Gateway checkpoint resource does not match")
		}
		checkpoints, ok := tx.(store.CheckpointStore)
		if !ok {
			return errors.New("checkpoint storage is required")
		}
		result, err = checkpoints.LoadCheckpoint(ctx, "Gateway", id, identityScanScope)
		return err
	})
	return result, err
}

// SaveIdentityCheckpoint locks the live Gateway while it saves the cursor.
// Deletion prevents later saves. Checkpoint writes do not change the public
// resource revision or emit a domain event. A stale save requires a new pass.
func (s *Service) SaveIdentityCheckpoint(ctx context.Context, p Principal, id string, version int64, after string) (store.ScanCheckpoint, error) {
	var result store.ScanCheckpoint
	if err := s.authorizeIdentityCheckpoint(p, id); err != nil {
		return result, err
	}
	if version < 0 || (after != "" && !validID(after)) {
		return result, ErrInvalid
	}
	err := s.repository.WithLockedResource(ctx, "Gateway", "id", id, func(ctx context.Context, tx store.Transaction, value any) error {
		row, ok := value.(model.Gateway)
		if !ok || row.ID != id {
			return errors.New("Gateway checkpoint resource does not match")
		}
		checkpoints, ok := tx.(store.CheckpointStore)
		if !ok {
			return errors.New("checkpoint storage is required")
		}
		if after != "" {
			reader, ok := tx.(store.CursorReader)
			if !ok {
				return errors.New("checkpoint source storage is required")
			}
			refs, err := reader.ReadCursor(ctx, "RoleBinding", "gateway_id", id, store.CursorOptions{Limit: 1, Deletion: store.CursorAll, ImplicitFilters: map[string]string{"id": after}})
			if err != nil {
				return err
			}
			rows, ok := refs.Items.([]model.RoleBinding)
			if !ok || refs.More || len(rows) != 1 || rows[0].ID != after || rows[0].GatewayID == nil || *rows[0].GatewayID != id {
				return ErrInvalid
			}
		}
		var err error
		result, err = checkpoints.SaveCheckpoint(ctx, "Gateway", id, identityScanScope, version, after)
		return err
	})
	return result, err
}
