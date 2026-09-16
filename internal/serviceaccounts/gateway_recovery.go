package serviceaccounts

import (
	"context"
	"errors"
	"strconv"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

const accountCleanupScope = "gateway-account-cleanup"

type accountCleanupItem struct {
	row       model.ServiceAccount
	journalID string
}

// RecoverGatewayCleanup supplies account policy to the generated durable scan.
// Only a committed Gateway deletion permits this work. Each account metadata
// change and audit share one commit. Completion requires a successful scan of
// rows and journals, plus a provider inventory check. Repeated calls check late
// provider effects.
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
	scopes, ok := s.repository.(storage.ResourceStateScopeStore)
	if !ok {
		return false, runtime.ErrScanContract
	}
	scope := gateways.AccountProviderStateScope(id)
	membership, err := scopes.LoadResourceStateScope(ctx, "ServiceAccount", scope)
	if err != nil {
		return false, err
	}
	sourceVersion := strconv.FormatInt(gateway.ResourceGeneration, 10) + ":" + strconv.FormatInt(membership.Revision, 10) + ":account-journals-v2"
	access, err := s.cleanupCheckpointAccess(gateway, accountCleanupScope, sourceVersion)
	if err != nil {
		return false, err
	}

	cursor, ok := s.repository.(storage.CursorReader)
	if !ok {
		return false, runtime.ErrScanContract
	}
	keys, ok := s.repository.(storage.ResourceStateKeyReader)
	if !ok {
		return false, runtime.ErrScanContract
	}
	retainedAccounts := func(ctx context.Context, after string, limit int) (runtime.CursorPage[accountCleanupItem], error) {
		page := runtime.CursorPage[accountCleanupItem]{}
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
			page.Items = append(page.Items, runtime.CursorItem[accountCleanupItem]{Cursor: row.ID, Value: accountCleanupItem{row: row}})
		}
		page.More = result.More
		return page, nil
	}
	retainedJournals := func(ctx context.Context, after string, limit int) (runtime.CursorPage[accountCleanupItem], error) {
		page := runtime.CursorPage[accountCleanupItem]{}
		result, err := keys.ListResourceStateKeys(ctx, "ServiceAccount", gateways.AccountProviderStateScope(id), after, limit)
		if err != nil {
			return page, err
		}
		for _, key := range result.Keys {
			if !validID(key.ResourceID) || key.Version < 1 {
				return page, runtime.ErrScanContract
			}
			page.Items = append(page.Items, runtime.CursorItem[accountCleanupItem]{Cursor: key.ResourceID, Value: accountCleanupItem{journalID: key.ResourceID}})
		}
		page.More = result.More
		return page, nil
	}
	source, err := runtime.SequenceCursorSources([]runtime.NamedCursorSource[accountCleanupItem]{
		{Name: "accounts", Read: retainedAccounts}, {Name: "journals", Read: retainedJournals},
	})
	if err != nil {
		return false, err
	}
	progress, err := runtime.ScanCycle(ctx, sourceVersion, access, source, func(ctx context.Context, item accountCleanupItem) error {
		if item.journalID == "" {
			return s.cleanupGatewayAccount(ctx, item.row)
		}
		call, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
		defer cancel()
		// An empty UUID tells the provider to load the retained, sealed identity.
		return errors.Join(s.provider.Delete(call, id, item.journalID, ""), call.Err())
	}, func(err error) bool { return !errors.Is(err, runtime.ErrScanContract) },
		runtime.ScanOptions{PageSize: 100, MaxPages: 1, PageTimeout: time.Second},
		runtime.ObservationOptions{WorkTimeout: 2 * time.Second, CommitTimeout: time.Second})
	complete := err == nil && progress.Complete
	if complete {
		// Discovery has its own saved cursor. It cannot replace retained cleanup.
		complete, err = s.recoverGatewayInventory(ctx, gateway)
	}
	observed := s.repository.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		if complete {
			scopes, ok := tx.(storage.ResourceStateScopeStore)
			if !ok {
				return runtime.ErrScanContract
			}
			// Reject a new key accepted during the scan or provider inventory. Closing
			// registration and recording cleanup must share this transaction.
			if _, err := scopes.SealResourceStateScope(ctx, "ServiceAccount", scope, membership.Revision); err != nil {
				return err
			}
		}
		return gateways.RecordCleanup(ctx, tx, id, gateway.ResourceVersion, "accounts", "", complete)
	})
	return complete && observed == nil, errors.Join(err, observed)
}

func (s *Service) cleanupGatewayAccount(ctx context.Context, row model.ServiceAccount) error {
	// One provider timeout must leave work time for independent later accounts.
	call, cancel := context.WithTimeout(ctx, 750*time.Millisecond)
	err := errors.Join(s.provider.Delete(call, row.GatewayID, row.ID, row.ClientUuid), call.Err())
	cancel()
	if err != nil {
		return err
	}
	if row.DeletedAt.Valid {
		return nil
	}
	err = s.repository.WithLockedResource(ctx, "ServiceAccount", "id", row.ID, func(ctx context.Context, tx storage.Transaction, value any) error {
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
