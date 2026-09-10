package gateways

import (
	"context"
	"errors"
	"strconv"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

const identityCycleScope = "identity-users-cycle"

type IdentityCycle struct {
	Checkpoint store.ScanCheckpoint
	Generation int64
}

func (s *Service) IdentityScanCycle(ctx context.Context, p Principal, id string) (IdentityCycle, error) {
	var result IdentityCycle
	if err := s.authorizeIdentityController(p, id); err != nil {
		return result, err
	}
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		value, err := tx.Get(ctx, "Gateway", id)
		if err != nil {
			return err
		}
		row, ok := value.(model.Gateway)
		if !ok || row.ID != id {
			return errors.New("identity cycle resource does not match")
		}
		checkpoints, ok := tx.(store.CheckpointStore)
		if !ok {
			return errors.New("checkpoint storage is required")
		}
		result.Checkpoint, err = checkpoints.LoadCheckpoint(ctx, "Gateway", id, identityCycleScope)
		if err != nil {
			return err
		}
		cycle, err := runtime.DecodeCycle(result.Checkpoint.After)
		if err != nil {
			return err
		}
		result.Generation = row.ResourceGeneration
		// Hide an old result without deleting its stored history or version.
		if cycle.Source != strconv.FormatInt(result.Generation, 10) {
			result.Checkpoint.After = ""
		}
		return nil
	})
	return result, err
}

// SaveIdentityScanCycle checks the desired generation and checkpoint revision
// while the live Gateway is locked. Grant writes use the same lock and reset
// the checkpoint. A scan cannot publish evidence from before a grant change.
func (s *Service) SaveIdentityScanCycle(ctx context.Context, p Principal, id string, version, generation int64, data string) (IdentityCycle, error) {
	var result IdentityCycle
	if err := s.authorizeIdentityController(p, id); err != nil {
		return result, err
	}
	cycle, err := runtime.DecodeCycle(data)
	if err != nil || data == "" || version < 0 || generation < 1 || cycle.Source != strconv.FormatInt(generation, 10) || (cycle.After != "" && !validID(cycle.After)) {
		return result, ErrInvalid
	}
	err = s.repository.WithLockedResource(ctx, "Gateway", "id", id, func(ctx context.Context, tx store.Transaction, value any) error {
		row, ok := value.(model.Gateway)
		if !ok || row.ID != id {
			return errors.New("identity cycle resource does not match")
		}
		if row.ResourceGeneration != generation {
			return store.ErrVersionConflict
		}
		checkpoints, ok := tx.(store.CheckpointStore)
		if !ok {
			return errors.New("checkpoint storage is required")
		}
		previous, err := checkpoints.LoadCheckpoint(ctx, "Gateway", id, identityCycleScope)
		if err != nil {
			return err
		}
		if previous.Version != version {
			return store.ErrCheckpointConflict
		}
		old, err := runtime.DecodeCycle(previous.After)
		if err != nil {
			return err
		}
		if runtime.ValidateCycleTransition(old, cycle) != nil || (old.Source == cycle.Source && !old.Complete && cycle.After < old.After) {
			return ErrInvalid
		}
		if err := validateIdentityCursor(ctx, tx, id, cycle.After); err != nil {
			return err
		}
		result.Checkpoint, err = checkpoints.SaveCheckpoint(ctx, "Gateway", id, identityCycleScope, version, data)
		result.Generation = generation
		return err
	})
	return result, err
}

// Invalidate the cycle in the same transaction as a changed grant and event.
// Retain the version so that an older in-flight writer receives a conflict.
func resetIdentityCycle(ctx context.Context, tx store.Transaction, id string) error {
	checkpoints, ok := tx.(store.CheckpointStore)
	if !ok {
		return errors.New("checkpoint storage is required")
	}
	previous, err := checkpoints.LoadCheckpoint(ctx, "Gateway", id, identityCycleScope)
	if err != nil {
		return err
	}
	_, err = checkpoints.SaveCheckpoint(ctx, "Gateway", id, identityCycleScope, previous.Version, "")
	return err
}

func validateIdentityCursor(ctx context.Context, tx store.Transaction, id, after string) error {
	if after == "" {
		return nil
	}
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
	return nil
}
