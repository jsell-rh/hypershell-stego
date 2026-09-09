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
