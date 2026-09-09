// Package users supplies the caller's stored identity through generated storage.
package users

import (
	"context"
	"errors"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

type Service struct{ repository store.Transactor }

func New(repository store.Transactor) (*Service, error) {
	if repository == nil {
		return nil, errors.New("user service requires storage")
	}
	return &Service{repository: repository}, nil
}

// Current resolves only a principal supplied by the generated token verifier.
// The stored issuer and subject identify the user; profile names do not.
func (s *Service) Current(ctx context.Context, principal gateways.Principal) (model.User, error) {
	var user model.User
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		resolved, err := gateways.ResolvePrincipal(ctx, tx, principal)
		if err != nil {
			return err
		}
		// Read stored timestamps after creation or a profile change.
		value, err := tx.Get(ctx, "User", resolved.ID)
		if err != nil {
			return err
		}
		var ok bool
		user, ok = value.(model.User)
		if !ok {
			return errors.New("unexpected user storage result")
		}
		return nil
	})
	if err != nil {
		return model.User{}, err
	}
	return user, nil
}
