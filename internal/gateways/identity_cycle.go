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
	Revision   int64
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
		result.Revision = row.ResourceVersion
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
func (s *Service) SaveIdentityScanCycle(ctx context.Context, p Principal, id string, version, generation, revision int64, data string) (IdentityCycle, error) {
	var result IdentityCycle
	if err := s.authorizeIdentityController(p, id); err != nil {
		return result, err
	}
	if revision < 1 {
		return result, ErrObservationRequired
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
		if row.ResourceGeneration != generation || row.ResourceVersion != revision {
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
		result.Revision = row.ResourceVersion
		if err != nil {
			return err
		}
		if !cycle.Failed && !cycle.Complete {
			return nil
		}
		update := store.ConditionUpdate{Name: "GrantsSynchronized", Status: "True", Reason: "GrantSyncComplete", Message: "Stored Gateway grant references were synchronized"}
		if cycle.Failed {
			update.Status = "Unknown"
			update.Reason = "GrantSyncIncomplete"
			update.Message = "One or more stored grant updates were not confirmed"
		}
		changed, err := writeGrantCondition(ctx, tx, row, update)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		if err := notifyGateway(tx, id, "Update", "gateway.updated"); err != nil {
			return err
		}
		stored, err := tx.Get(ctx, "Gateway", id)
		if err != nil {
			return err
		}
		current, ok := stored.(model.Gateway)
		if !ok || current.ID != id {
			return errors.New("identity cycle resource does not match")
		}
		result.Revision = current.ResourceVersion
		return nil
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
	if _, err = checkpoints.SaveCheckpoint(ctx, "Gateway", id, identityCycleScope, previous.Version, ""); err != nil {
		return err
	}
	value, err := tx.Get(ctx, "Gateway", id)
	if err != nil {
		return err
	}
	row, ok := value.(model.Gateway)
	if !ok || row.ID != id {
		return errors.New("identity cycle resource does not match")
	}
	_, err = writeGrantCondition(ctx, tx, row, store.ConditionUpdate{Name: "GrantsSynchronized", Status: "Unknown", Reason: "GrantsChanged", Message: "Gateway grant changes require a new scan"})
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

// Partial successful passes preserve the last complete observation. A failed
// pass removes positive evidence. Each owner retains its separate condition.
func writeGrantCondition(ctx context.Context, tx store.Transaction, row model.Gateway, update store.ConditionUpdate) (bool, error) {
	values, err := row.Conditions()
	if err != nil {
		return false, err
	}
	current := values["identity_users"]["GrantsSynchronized"]
	if current.Current && current.Status == update.Status && current.Reason == update.Reason && current.Message == update.Message {
		return false, nil
	}
	writer, ok := tx.(store.ConditionWriter)
	if !ok {
		return false, errors.New("identity storage requires conditions")
	}
	if err := writer.ObserveConditionsIfVersion(ctx, "Gateway", row.ID, row.ResourceVersion, "identity_users", []store.ConditionUpdate{update}); err != nil {
		return false, err
	}
	return true, nil
}
