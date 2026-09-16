package gateways

import (
	"context"
	"errors"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// PublicState supplies the public lifecycle fields after observation projection.
func PublicState(row model.Gateway) model.Gateway {
	row = row.CurrentObservations()
	if row.DeletedAt.Valid && row.DeletionFinalizedAt == nil {
		phase, status := "Deleting", "Gateway cleanup is in progress"
		row.Phase = &phase
		row.Status = &status
	}
	return row
}

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
		writer, ok := tx.(store.CleanupWriter)
		if !ok {
			return errors.New("cleanup storage is required")
		}
		if err := writer.ObserveCleanupIfVersion(ctx, "Gateway", id, version, owner, complete); err != nil {
			return err
		}
	case "workload", "sql":
		if !validID(target) {
			return ErrInvalid
		}
		writer, ok := tx.(store.TargetCleanupWriter)
		if !ok {
			return errors.New("target cleanup storage is required")
		}
		if err := writer.ObserveTargetCleanupIfVersion(ctx, "Gateway", id, version, owner, target, complete); err != nil {
			return err
		}
	default:
		return ErrInvalid
	}
	reader, ok := tx.(store.RetainedReader)
	if !ok {
		return errors.New("retained storage is required")
	}
	value, err := reader.GetRetained(ctx, "Gateway", id)
	if err != nil {
		return err
	}
	row, ok := value.(model.Gateway)
	if !ok || row.ID != id || !row.DeletedAt.Valid {
		return errors.New("cleanup resource does not match")
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
	return notifyGateway(tx, id, "Update", "gateway.updated")
}
