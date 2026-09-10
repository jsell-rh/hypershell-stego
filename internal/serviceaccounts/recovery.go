package serviceaccounts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

type recoveryTask struct {
	row     model.ServiceAccount
	deleted bool
}

// Run supplies account rules to the generated page scheduler. It never provisions
// a client, raises an existing role, or reconstructs a credential.
func (s *Service) Run(ctx context.Context) error {
	if ctx == nil {
		return errors.New("service-account recovery requires a context")
	}
	if s.provider == nil {
		<-ctx.Done()
		return nil
	}
	groups := []runtime.SweepGroup[recoveryTask]{}
	for _, state := range []string{"error", "deleting", "revoking", "provisioning", "ready", "degraded", "drift", "revoked", "expired"} {
		streams := []runtime.SweepStream[recoveryTask]{s.recoveryStream(state, false)}
		if state == "error" || state == "deleting" || state == "provisioning" {
			streams = append(streams, s.recoveryStream(state, true))
		}
		groups = append(groups, runtime.SweepGroup[recoveryTask]{Name: state, Streams: streams})
	}
	return runtime.RunSweep(ctx, groups, s.recoverTask, runtime.SweepOptions{
		Workers: 8, PageSize: 100, MaxPagesPerCycle: 10000, PassTimeout: 4 * time.Second, Interval: time.Second,
		// Provider and storage outages remain retryable. Invalid source data stops
		// the worker. Recovery actions still enforce their current access rules.
		Terminal: func(err error) bool { return errors.Is(err, runtime.ErrSweepContract) },
		Observe: func(event runtime.SweepEvent) {
			if event.Err != nil {
				slog.Warn("service-account recovery needs another pass", "group", event.Group, "stream", event.Stream, "started", event.Started, "failed", event.Failed)
			}
		},
	})
}
func (s *Service) recoveryStream(state string, deleted bool) runtime.SweepStream[recoveryTask] {
	name := "live"
	if deleted {
		name = "deleted"
	}
	return runtime.SweepStream[recoveryTask]{Name: name, Page: func(ctx context.Context, after string, limit int) (runtime.SweepPage[recoveryTask], error) {
		page := runtime.SweepPage[recoveryTask]{}
		if after != "" && !validID(after) {
			return page, fmt.Errorf("%w: invalid account cursor", runtime.ErrSweepContract)
		}
		queryState := state
		if state == "drift" {
			queryState = "ready"
		}
		condition := ""
		if state == "ready" {
			condition = "expires_at <= '" + s.now().UTC().Format(time.RFC3339Nano) + "'"
		}
		if state == "provisioning" {
			condition = "created_time <= '" + s.now().Add(-ReclaimAfter).UTC().Format(time.RFC3339Nano) + "'"
		}
		reader, ok := s.repository.(storage.CursorReader)
		if !ok {
			return page, fmt.Errorf("%w: account storage does not support cursor reads", runtime.ErrSweepContract)
		}
		mode := storage.CursorLive
		if deleted {
			mode = storage.CursorDeleted
		}
		result, err := reader.ReadCursor(ctx, "ServiceAccount", "status", queryState, storage.CursorOptions{AfterID: after, Limit: limit, Search: condition, Deletion: mode})
		if err != nil {
			if errors.Is(err, storage.ErrCursor) || errors.Is(err, storage.ErrCursorResult) {
				return page, fmt.Errorf("%w: invalid account storage page", runtime.ErrSweepContract)
			}
			return page, err
		}
		rows, ok := result.Items.([]model.ServiceAccount)
		if !ok || len(rows) > limit {
			return page, fmt.Errorf("%w: unexpected account result", runtime.ErrSweepContract)
		}
		for _, row := range rows {
			if !validID(row.ID) {
				return page, fmt.Errorf("%w: invalid account ID", runtime.ErrSweepContract)
			}
			page.Items = append(page.Items, runtime.SweepItem[recoveryTask]{Cursor: row.ID, Value: recoveryTask{row: row, deleted: deleted}})
		}
		page.More = result.More
		return page, nil
	}}
}
func (s *Service) recoverTask(ctx context.Context, task recoveryTask) error {
	row := task.row
	// Only the stream for the stored deletion state can act.
	if row.DeletedAt.Valid != task.deleted {
		return nil
	}
	if row.DeletedAt.Valid {
		// Deleted records cannot be restored. Stable IDs identify late provider
		// resources even when the former provider UUID is no longer valid.
		return s.provider.Delete(ctx, row.GatewayID, row.ID, "")
	}
	return s.Recover(ctx, row.GatewayID, row.ID)
}
