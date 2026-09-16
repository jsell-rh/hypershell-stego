package serviceaccounts

import (
	"context"
	"errors"
	"strconv"
	"time"

	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

const accountCleanupScope = "gateway-account-cleanup"

// RecoverGatewayCleanup supplies account policy to the generated durable scan.
// Only a committed Gateway deletion permits this work. Each account metadata
// change and audit share one commit. Complete requires both a successful retained
// scan and a provider inventory check. Repeated calls check late provider effects.
func (s *Service) RecoverGatewayCleanup(ctx context.Context, id string) (bool, error) {
	if ctx == nil || !validID(id) {
		return false, runtime.ErrScanContract
	}
	reader, ok := s.repository.(storage.RetainedReader)
	if !ok {
		return false, runtime.ErrScanContract
	}
	value, err := reader.GetRetained(ctx, "Gateway", id)
	if err != nil {
		return false, err
	}
	gateway, ok := value.(model.Gateway)
	if !ok || gateway.ID != id || !gateway.DeletedAt.Valid {
		return false, runtime.ErrScanContract
	}
	if s.provider == nil {
		return false, ErrUnavailable
	}
	checkpoints, ok := s.repository.(storage.CheckpointStore)
	if !ok {
		return false, runtime.ErrScanContract
	}
	sourceVersion := strconv.FormatInt(gateway.ResourceGeneration, 10)
	access := runtime.CheckpointAccess{
		Load: func(ctx context.Context) (runtime.Checkpoint, error) {
			saved, err := checkpoints.LoadCheckpoint(ctx, "Gateway", id, accountCleanupScope)
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
				saved, err := progressStore.LoadCheckpoint(ctx, "Gateway", id, accountCleanupScope)
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
				_, err = progressStore.SaveCheckpoint(ctx, "Gateway", id, accountCleanupScope, version, data)
				return err
			})
		},
	}
	cursor, ok := s.repository.(storage.CursorReader)
	if !ok {
		return false, runtime.ErrScanContract
	}
	progress, err := runtime.ScanCycle(ctx, sourceVersion, access, func(ctx context.Context, after string, limit int) (runtime.CursorPage[model.ServiceAccount], error) {
		page := runtime.CursorPage[model.ServiceAccount]{}
		result, err := cursor.ReadCursor(ctx, "ServiceAccount", "gateway_id", id, storage.CursorOptions{AfterID: after, Limit: limit, Deletion: storage.CursorAll})
		if err != nil {
			return page, err
		}
		rows, ok := result.Items.([]model.ServiceAccount)
		if !ok {
			return page, runtime.ErrScanContract
		}
		for _, row := range rows {
			if row.GatewayID != id || !validID(row.ID) {
				return page, runtime.ErrScanContract
			}
			page.Items = append(page.Items, runtime.CursorItem[model.ServiceAccount]{Cursor: row.ID, Value: row})
		}
		page.More = result.More
		return page, nil
	}, s.cleanupGatewayAccount, func(err error) bool { return !errors.Is(err, runtime.ErrScanContract) },
		runtime.ScanOptions{PageSize: 100, MaxPages: 1, PageTimeout: time.Second},
		runtime.ObservationOptions{WorkTimeout: 2 * time.Second, CommitTimeout: time.Second})
	if err != nil || !progress.Complete {
		return false, err
	}
	// Inventory has a separate budget. A failed inventory check does not discard
	// the retained-account checkpoint. It cannot authorize completion by itself.
	call, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := s.provider.DeleteGateway(call, id); err != nil {
		return false, err
	}
	return true, nil
}

func (s *Service) cleanupGatewayAccount(ctx context.Context, row model.ServiceAccount) error {
	if err := s.provider.Delete(ctx, row.GatewayID, row.ID, row.ClientUuid); err != nil {
		return err
	}
	if row.DeletedAt.Valid {
		return nil
	}
	err := s.repository.WithLockedResource(ctx, "ServiceAccount", "id", row.ID, func(ctx context.Context, tx storage.Transaction, value any) error {
		current, ok := value.(model.ServiceAccount)
		if !ok || current.ID != row.ID || current.GatewayID != row.GatewayID {
			return runtime.ErrScanContract
		}
		current.Status = "deleting"
		current.Active = false
		current.ActiveName = nil
		current.LastError = nil
		if current.RevokedAt == nil {
			now := s.now().UTC()
			current.RevokedAt = &now
		}
		if err := save(ctx, tx, &current); err != nil {
			return err
		}
		if err := audit(ctx, tx, current, "system", "gateway_cleanup", "succeeded"); err != nil {
			return err
		}
		return tx.Delete(ctx, "ServiceAccount", current.ID)
	})
	// Another cleanup worker can commit the same metadata first. The live row lock
	// prevents a duplicate success audit. Provider deletion is safe to repeat.
	if errors.Is(err, storage.ErrNotFound) {
		return nil
	}
	return err
}
