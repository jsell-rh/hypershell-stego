package gateways

import (
	"context"
	"errors"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// EventGateway applies current access grants, including for a deletion event.
// The event must match the stored deletion state before data can be returned.
func (s *Service) EventGateway(ctx context.Context, p Principal, id string, deleted bool) (model.Gateway, error) {
	if err := validatePrincipal(p); err != nil {
		return model.Gateway{}, err
	}
	if !validID(id) {
		return model.Gateway{}, store.ErrNotFound
	}
	result, err := s.list(ctx, p, id, 1, 1, "", nil, deleted)
	if err != nil {
		return model.Gateway{}, err
	}
	rows, ok := result.Items.([]model.Gateway)
	if !ok {
		return model.Gateway{}, errors.New("unexpected Gateway storage result")
	}
	if len(rows) != 1 || rows[0].DeletedAt.Valid != deleted {
		return model.Gateway{}, store.ErrNotFound
	}
	return rows[0], nil
}
