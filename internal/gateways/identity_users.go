package gateways

import (
	"context"
	"errors"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

var ErrUnboundUser = errors.New("stored user has no verified provider identity")

type IdentityUser struct{ GatewayID, UserID, Issuer, Subject, Role string }

func (s *Service) checkIdentityReader(p Principal, id string) error {
	if err := validatePrincipal(p); err != nil {
		return err
	}
	if !s.isControlPlane(p) {
		return ErrForbidden
	}
	if !validID(id) {
		return store.ErrNotFound
	}
	return nil
}

// IdentityUsers includes grant history so that recovery can remove old roles.
func (s *Service) IdentityUsers(ctx context.Context, p Principal, id string, page int) ([]string, bool, error) {
	if err := s.checkIdentityReader(p, id); err != nil {
		return nil, false, err
	}
	if page < 1 || page > 100 {
		return nil, false, ErrInvalid
	}
	var ids []string
	var more bool
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		if _, err := tx.Get(ctx, "Gateway", id); err != nil {
			return err
		}
		result, err := tx.List(ctx, "RoleBinding", "gateway_id", id, store.ListOptions{Page: page, Size: 100, IncludeDeleted: true, OrderBy: []store.OrderByField{{Field: "id", Direction: "asc"}}})
		if err != nil {
			return err
		}
		rows, ok := result.Items.([]model.RoleBinding)
		if !ok {
			return errors.New("unexpected grant storage result")
		}
		for _, row := range rows {
			ids = append(ids, row.UserID)
		}
		more = int64(page)*100 < result.Total
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return ids, more, nil
}

// IdentityUserState recomputes the union of current owner and viewer grants.
func (s *Service) IdentityUserState(ctx context.Context, p Principal, gatewayID, userID string) (IdentityUser, error) {
	empty := IdentityUser{}
	if err := s.checkIdentityReader(p, gatewayID); err != nil {
		return empty, err
	}
	if !validID(userID) {
		return empty, store.ErrNotFound
	}
	var state IdentityUser
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		if _, err := tx.Get(ctx, "Gateway", gatewayID); err != nil {
			return err
		}
		result, err := tx.List(ctx, "User", "id", userID, store.ListOptions{Page: 1, Size: 1, IncludeDeleted: true})
		if err != nil {
			return err
		}
		rows, ok := result.Items.([]model.User)
		if !ok {
			return errors.New("unexpected user storage result")
		}
		if len(rows) != 1 {
			return store.ErrNotFound
		}
		user := rows[0]
		if user.Issuer == nil || user.Subject == nil || *user.Issuer == "" || *user.Subject == "" {
			return ErrUnboundUser
		}
		state = IdentityUser{GatewayID: gatewayID, UserID: userID, Issuer: *user.Issuer, Subject: *user.Subject}
		if user.DeletedAt.Valid {
			return nil
		}
		for _, name := range []string{"gateway:owner", "gateway:viewer"} {
			role, err := findRole(ctx, tx, name)
			if err != nil {
				return err
			}
			grants, err := tx.List(ctx, "RoleBinding", "gateway_id", gatewayID, store.ListOptions{Page: 1, CountOnly: true, ImplicitFilters: map[string]string{"user_id": userID, "role_id": role.ID, "scope": "gateway"}})
			if err != nil {
				return err
			}
			if grants.Total > 0 {
				state.Role = name
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return empty, err
	}
	return state, nil
}
