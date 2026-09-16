package gateways

import (
	"context"
	"errors"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// RecordCleanup is an internal application commit boundary. External callers
// must first pass the controller authorization in ObserveCleanup. The account
// worker supplies only its own accounts observation. No provider work runs here.
func RecordCleanup(ctx context.Context, tx store.Transaction, id string, version int64, owner, target string, complete bool) error {
	if !validID(id) || version < 1 {
		return ErrInvalid
	}
	switch owner {
	case "accounts", "identity":
		if target != "" {
			return ErrInvalid
		}
	case "workload", "sql":
		if !validID(target) {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	reader, ok := tx.(store.RetainedReader)
	if !ok {
		return errors.New("retained storage is required")
	}
	read := func() (model.Gateway, error) {
		value, err := reader.GetRetained(ctx, "Gateway", id)
		if err != nil {
			return model.Gateway{}, err
		}
		row, ok := value.(model.Gateway)
		if !ok || row.ID != id || !row.DeletedAt.Valid {
			return model.Gateway{}, errors.New("cleanup resource does not match")
		}
		return row, nil
	}
	row, err := read()
	if err != nil {
		return err
	}
	if row.ResourceVersion != version {
		return store.ErrVersionConflict
	}
	states, err := row.CleanupObservations()
	if err != nil {
		return err
	}
	changed := states[owner] != complete
	if target != "" {
		targets, err := row.CleanupTargets()
		if err != nil {
			return err
		}
		prior, present := targets[owner][target]
		if !present {
			return store.ErrVersionConflict
		}
		changed = prior != complete
	}
	if changed {
		if target == "" {
			writer, ok := tx.(store.CleanupWriter)
			if !ok {
				return errors.New("cleanup storage is required")
			}
			err = writer.ObserveCleanupIfVersion(ctx, "Gateway", id, version, owner, complete)
		} else {
			writer, ok := tx.(store.TargetCleanupWriter)
			if !ok {
				return errors.New("target cleanup storage is required")
			}
			err = writer.ObserveTargetCleanupIfVersion(ctx, "Gateway", id, version, owner, target, complete)
		}
		if err != nil {
			return err
		}
		row, err = read()
		if err != nil {
			return err
		}
	}
	pending, err := row.PendingCleanup()
	if err != nil {
		return err
	}
	if len(pending) == 0 && row.DeletionFinalizedAt == nil {
		writer, ok := tx.(store.DeletionFinalizer)
		if !ok {
			return errors.New("deletion finalization storage is required")
		}
		if err := writer.FinalizeDeletionIfVersion(ctx, "Gateway", id, row.ResourceVersion); err != nil {
			return err
		}
		return notifyGateway(tx, id, "Delete", "gateway.deleted")
	}
	// Repeated recovery must not change revisions or publish duplicate state.
	if !changed {
		return nil
	}
	return notifyGateway(tx, id, "Update", "gateway.updated")
}
