package serviceaccounts

import (
	"context"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

func (s *Service) cleanupCheckpointAccess(gateway model.Gateway, checkpointScope, sourceVersion string) (runtime.CheckpointAccess, error) {
	checkpoints, ok := s.repository.(storage.CheckpointStore)
	if !ok {
		return runtime.CheckpointAccess{}, runtime.ErrScanContract
	}
	id := gateway.ID
	return runtime.CheckpointAccess{
		Load: func(ctx context.Context) (runtime.Checkpoint, error) {
			saved, err := checkpoints.LoadCheckpoint(ctx, "Gateway", id, checkpointScope)
			return runtime.Checkpoint{After: saved.After, Version: saved.Version}, err
		},
		Save: func(ctx context.Context, version int64, data string) error {
			return s.repository.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
				retained, ok := tx.(storage.RetainedReader)
				if !ok {
					return runtime.ErrScanContract
				}
				progressStore, ok := tx.(storage.CheckpointStore)
				if !ok {
					return runtime.ErrScanContract
				}
				value, err := retained.GetRetained(ctx, "Gateway", id)
				if err != nil {
					return err
				}
				current, ok := value.(model.Gateway)
				if !ok || current.ID != id || !current.DeletedAt.Valid || current.ResourceGeneration != gateway.ResourceGeneration {
					return storage.ErrVersionConflict
				}
				saved, err := progressStore.LoadCheckpoint(ctx, "Gateway", id, checkpointScope)
				if err != nil {
					return err
				}
				if saved.Version != version {
					return storage.ErrCheckpointConflict
				}
				old, err := runtime.DecodeCycle(saved.After)
				if err != nil {
					return err
				}
				next, err := runtime.DecodeCycle(data)
				if err != nil {
					return err
				}
				if next.Source != sourceVersion {
					return runtime.ErrScanContract
				}
				if err := runtime.ValidateCycleTransition(old, next); err != nil {
					return err
				}
				_, err = progressStore.SaveCheckpoint(ctx, "Gateway", id, checkpointScope, version, data)
				return err
			})
		},
	}, nil
}
