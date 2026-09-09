// Package roles reads the role catalog. Transport adapters require authentication.
package roles

import (
	"context"
	"errors"
	"slices"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

type Service struct{ repository store.Transactor }
type Query struct {
	Page, Size int
	Search     string
	OrderBy    []store.OrderByField
}

func New(repository store.Transactor) (*Service, error) {
	if repository == nil {
		return nil, errors.New("role catalog requires storage")
	}
	return &Service{repository: repository}, nil
}
func (s *Service) Get(ctx context.Context, id string) (model.Role, error) {
	key, err := ksuid.Parse(id)
	if err != nil || key == ksuid.Nil || key.String() != id {
		return model.Role{}, store.ErrNotFound
	}
	var row model.Role
	err = s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		value, err := tx.Get(ctx, "Role", id)
		if err != nil {
			return err
		}
		var ok bool
		row, ok = value.(model.Role)
		if !ok {
			return errors.New("unexpected role storage result")
		}
		return nil
	})
	if err != nil {
		return model.Role{}, err
	}
	return row, nil
}
func (s *Service) List(ctx context.Context, q Query) (store.ListResult, error) {
	if q.Page < 1 || q.Page > 1000000 || q.Size < 0 || q.Size > 100 {
		return store.ListResult{}, gateways.ErrInvalid
	}
	order := append([]store.OrderByField(nil), q.OrderBy...)
	if !slices.ContainsFunc(order, func(v store.OrderByField) bool { return v.Field == "id" }) {
		order = append(order, store.OrderByField{Field: "id", Direction: "asc"})
	}
	var result store.ListResult
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		var err error
		result, err = tx.List(ctx, "Role", "", "", store.ListOptions{Page: q.Page, Size: q.Size, CountOnly: q.Size == 0, Search: q.Search, OrderBy: order})
		return err
	})
	if err != nil {
		return store.ListResult{}, err
	}
	return result, nil
}
