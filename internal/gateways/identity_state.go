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
	result, err := s.list(ctx, p, id, 1, 1, "", nil, true)
	if err != nil {
		return model.Gateway{}, err
	}
	rows, ok := result.Items.([]model.Gateway)
	if !ok {
		return model.Gateway{}, errors.New("unexpected Gateway storage result")
	}
	if len(rows) != 1 {
		return model.Gateway{}, store.ErrNotFound
	}
	return rows[0], nil
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
