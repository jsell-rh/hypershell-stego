package gateways

import (
	"context"
	"errors"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

type SQLComponent int32

const (
	SQLComponentGateway SQLComponent = iota
	SQLComponentConsole
)

func validSQLComponent(component SQLComponent) bool {
	return component == SQLComponentGateway || component == SQLComponentConsole
}
func consoleSQLCompletionScope(cluster string) string { return "sql-complete:console:" + cluster }
func sqlStateScope(cluster string, component SQLComponent) string {
	if component == SQLComponentConsole {
		return "sql-state:console:" + cluster
	}
	return "sql-state:" + cluster
}

func sqlStatePlacement(value any, id, cluster string) (model.Gateway, error) {
	row, ok := value.(model.Gateway)
	if !ok || row.ID != id {
		return row, errors.New("SQL state resource does not match")
	}
	history, err := row.CleanupTargets()
	if err != nil {
		return row, err
	}
	if _, recorded := history["sql"][cluster]; !recorded || row.ClusterID != cluster {
		return row, ErrForbidden
	}
	return row, nil
}

func (s *Service) authorizeSQLState(p Principal, id, cluster string, component SQLComponent, close bool) error {
	if !validID(id) || !validID(cluster) || !validSQLComponent(component) {
		return ErrInvalid
	}
	if close {
		return s.AuthorizeCleanup(p, "Gateway", "sql", cluster)
	}
	return s.AuthorizeControllerWrite(p, "Gateway", "configure.sql", cluster)
}

// SQLStateBinding is a retained read for the exact configured controller target.
// Absence is not a cleanup result. Cleanup must close registration first.
func (s *Service) SQLStateBinding(ctx context.Context, p Principal, id, cluster string, component SQLComponent) (result store.EffectBinding, err error) {
	if !validID(id) || !validID(cluster) || !validSQLComponent(component) {
		return result, ErrInvalid
	}
	if s.AuthorizeControllerWrite(p, "Gateway", "configure.sql", cluster) != nil {
		if err = s.AuthorizeCleanup(p, "Gateway", "sql", cluster); err != nil {
			return result, err
		}
	}
	err = s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		reader, ok := tx.(store.RetainedReader)
		if !ok {
			return errors.New("SQL state requires retained storage")
		}
		value, err := reader.GetRetained(ctx, "Gateway", id)
		if err != nil {
			return err
		}
		if _, err = sqlStatePlacement(value, id, cluster); err != nil {
			return err
		}
		bindings, ok := tx.(store.EffectBindingStore)
		if !ok {
			return errors.New("SQL state requires effect binding storage")
		}
		result, err = bindings.LoadEffectBinding(ctx, "Gateway", id, sqlStateScope(cluster, component))
		return err
	})
	return result, err
}

// BindSQLState holds the live resource lock until the binding commits. The
// worker must not use the retained state for SQL before this call succeeds.
func (s *Service) BindSQLState(ctx context.Context, p Principal, id, cluster, digest string, version int64, component SQLComponent) (result store.EffectBinding, err error) {
	if err = s.authorizeSQLState(p, id, cluster, component, false); err != nil {
		return result, err
	}
	if version < 1 {
		return result, ErrObservationRequired
	}
	err = s.repository.WithLockedResource(ctx, "Gateway", "id", id, func(ctx context.Context, tx store.Transaction, value any) error {
		row, err := sqlStatePlacement(value, id, cluster)
		if err != nil {
			return err
		}
		if row.DeletedAt.Valid || row.ResourceVersion != version {
			return store.ErrVersionConflict
		}
		bindings, ok := tx.(store.EffectBindingStore)
		if !ok {
			return errors.New("SQL state requires effect binding storage")
		}
		result, err = bindings.BindEffect(ctx, "Gateway", id, sqlStateScope(cluster, component), digest)
		return err
	})
	return result, err
}

// CloseSQLState requires durable deletion. Deletion cannot be reversed. A
// concurrent registration must have committed before the Gateway was deleted,
// because registration holds its live row lock. STEGO retains that digest.
func (s *Service) CloseSQLState(ctx context.Context, p Principal, id, cluster string, component SQLComponent) (store.EffectBinding, error) {
	return s.closeSQLState(ctx, p, id, cluster, component, false)
}

// CompleteConsoleSQLCleanup records the worker's successful database removal.
// Closing registration alone is not proof that external cleanup has finished.
func (s *Service) CompleteConsoleSQLCleanup(ctx context.Context, p Principal, id, cluster string) (store.EffectBinding, error) {
	return s.closeSQLState(ctx, p, id, cluster, SQLComponentConsole, true)
}
func (s *Service) closeSQLState(ctx context.Context, p Principal, id, cluster string, component SQLComponent, complete bool) (result store.EffectBinding, err error) {
	if err = s.authorizeSQLState(p, id, cluster, component, true); err != nil {
		return result, err
	}
	err = s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		reader, ok := tx.(store.RetainedReader)
		if !ok {
			return errors.New("SQL state requires retained storage")
		}
		value, err := reader.GetRetained(ctx, "Gateway", id)
		if err != nil {
			return err
		}
		row, err := sqlStatePlacement(value, id, cluster)
		if err != nil {
			return err
		}
		if !row.DeletedAt.Valid {
			return store.ErrVersionConflict
		}
		bindings, ok := tx.(store.EffectBindingStore)
		if !ok {
			return errors.New("SQL state requires effect binding storage")
		}
		if complete {
			result, err = bindings.LoadEffectBinding(ctx, "Gateway", id, sqlStateScope(cluster, component))
			if err != nil {
				return err
			}
			if !result.Present || !result.Closed {
				return store.ErrEffectBindingConflict
			}
			prior, err := bindings.LoadEffectBinding(ctx, "Gateway", id, consoleSQLCompletionScope(cluster))
			if err != nil {
				return err
			}
			if prior.Closed {
				if prior.Digest != result.Digest {
					return store.ErrEffectBindingConflict
				}
				return nil
			}
			if result.Digest != "" {
				if _, err = bindings.BindEffect(ctx, "Gateway", id, consoleSQLCompletionScope(cluster), result.Digest); err != nil {
					return err
				}
			}
			_, err = bindings.CloseEffectBinding(ctx, "Gateway", id, consoleSQLCompletionScope(cluster))
			return err
		}
		result, err = bindings.CloseEffectBinding(ctx, "Gateway", id, sqlStateScope(cluster, component))
		return err
	})
	return result, err
}
