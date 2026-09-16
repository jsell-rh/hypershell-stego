package gateways

import (
	"context"
	"errors"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// IdentityState includes a retained deletion row. Only a configured control-plane
// subject can read it. An absent or denied row is not evidence of deletion.
func (s *Service) IdentityState(ctx context.Context, p Principal, id string) (model.Gateway, error) {
	if err := validatePrincipal(p); err != nil {
		return model.Gateway{}, err
	}
	if !s.isControlPlane(p) {
		return model.Gateway{}, ErrForbidden
	}
	if !validID(id) {
		return model.Gateway{}, store.ErrNotFound
	}
	var row model.Gateway
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		reader, ok := tx.(store.RetainedReader)
		if !ok {
			return errors.New("Gateway storage does not support retained reads")
		}
		value, err := reader.GetRetained(ctx, "Gateway", id)
		if err != nil {
			return err
		}
		row, ok = value.(model.Gateway)
		if !ok {
			return errors.New("unexpected Gateway storage result")
		}
		return nil
	})
	return row, err
}

// ReconcileIDs supplies bounded recovery pages without resource contents. The
// cursor must be a canonical KSUID before the generated storage call.
func (s *Service) ReconcileIDs(ctx context.Context, p Principal, after string) ([]string, error) {
	if err := validatePrincipal(p); err != nil {
		return nil, err
	}
	if !s.isControlPlane(p) {
		return nil, ErrForbidden
	}
	if after != "" && !validID(after) {
		return nil, ErrInvalid
	}
	options := store.CursorOptions{AfterID: after, Limit: 100, Deletion: store.CursorAll, Fields: []string{"id"}}
	var ids []string
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		reader, ok := tx.(store.CursorReader)
		if !ok {
			return errors.New("Gateway storage does not support cursor reads")
		}
		result, err := reader.ReadCursor(ctx, "Gateway", "", "", options)
		if err != nil {
			return err
		}
		rows, ok := result.Items.([]model.Gateway)
		if !ok {
			return errors.New("unexpected Gateway recovery storage result")
		}
		for _, row := range rows {
			ids = append(ids, row.ID)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

// ObserveCleanup authorizes a controller observation before its atomic commit.
func (s *Service) ObserveCleanup(ctx context.Context, p Principal, id string, version int64, owner, target string, complete bool) error {
	if err := validatePrincipal(p); err != nil {
		return err
	}
	if !s.isControlPlane(p) || (owner != "identity" && owner != "workload" && owner != "sql") || (owner == "identity" && target != "") || ((owner == "workload" || owner == "sql") && !validID(target)) {
		return ErrForbidden
	}
	if err := s.AuthorizeCleanup(p, "Gateway", owner, target); err != nil {
		return err
	}
	if !validID(id) {
		return store.ErrNotFound
	}
	if version < 1 {
		return ErrObservationRequired
	}
	return s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		return RecordCleanup(ctx, tx, id, version, owner, target, complete)
	})
}
