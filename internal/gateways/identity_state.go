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
// cursor must be a canonical KSUID before it enters the generated search parser.
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
	options := store.ListOptions{Page: 1, Size: 100, IncludeDeleted: true, Fields: []string{"id"}, OrderBy: []store.OrderByField{{Field: "id", Direction: "asc"}}}
	if after != "" {
		options.Search = "id > '" + after + "'"
	}
	var ids []string
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		result, err := tx.List(ctx, "Gateway", "", "", options)
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

// ObserveCleanup commits the observation and its deletion notice together.
func (s *Service) ObserveCleanup(ctx context.Context, p Principal, id string, version int64, owner string, complete bool) error {
	if err := validatePrincipal(p); err != nil {
		return err
	}
	if !s.isControlPlane(p) || owner != "identity" {
		return ErrForbidden
	}
	if !validID(id) {
		return store.ErrNotFound
	}
	if version < 1 {
		return ErrObservationRequired
	}
	return s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		writer, ok := tx.(store.CleanupWriter)
		if !ok {
			return errors.New("Gateway storage does not support cleanup observations")
		}
		if err := writer.ObserveCleanupIfVersion(ctx, "Gateway", id, version, owner, complete); err != nil {
			return err
		}
		return notifyGateway(tx, id, "Delete", "gateway.deleted")
	})
}
