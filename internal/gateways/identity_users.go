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
		reader, ok := tx.(store.CursorReader)
		if !ok {
			return errors.New("identity storage does not support bounded reads")
		}
		result, err := reader.ReadCursor(ctx, "User", "id", userID, store.CursorOptions{Limit: 1, Deletion: store.CursorAll})
		if err != nil {
			return err
		}
		rows, ok := result.Items.([]model.User)
		if !ok || result.More || len(rows) > 1 {
			return errors.New("unexpected user storage result")
		}
		if len(rows) != 1 {
			return store.ErrNotFound
		}
		user := rows[0]
		if user.ID != userID {
			return errors.New("stored user does not match the request")
		}
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
			grants, err := reader.ReadCursor(ctx, "RoleBinding", "gateway_id", gatewayID, store.CursorOptions{Limit: 1, ImplicitFilters: map[string]string{"user_id": userID, "role_id": role.ID, "scope": "gateway"}})
			if err != nil {
				return err
			}
			rows, ok := grants.Items.([]model.RoleBinding)
			if !ok || len(rows) > 1 {
				return errors.New("unexpected grant storage result")
			}
			if len(rows) != 0 {
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

// IdentityUserReferences returns one bounded page in database grant-ID order.
// Deleted grants remain eligible so a later pass can remove provider access.
type IdentityUserReference struct{ GrantID, UserID string }

func (s *Service) IdentityUserReferences(ctx context.Context, p Principal, id, after string, limit int) ([]IdentityUserReference, bool, error) {
	if err := s.checkIdentityReader(p, id); err != nil {
		return nil, false, err
	}
	if (after != "" && !validID(after)) || limit < 1 || limit > 100 {
		return nil, false, ErrInvalid
	}
	var references []IdentityUserReference
	var more bool
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		if _, err := tx.Get(ctx, "Gateway", id); err != nil {
			return err
		}
		reader, ok := tx.(store.CursorReader)
		if !ok {
			return errors.New("identity storage does not support cursor reads")
		}
		result, err := reader.ReadCursor(ctx, "RoleBinding", "gateway_id", id, store.CursorOptions{AfterID: after, Limit: limit, Deletion: store.CursorAll, Fields: []string{"id", "user_id"}})
		if err != nil {
			return err
		}
		rows, ok := result.Items.([]model.RoleBinding)
		if !ok || len(rows) > limit || (result.More && len(rows) == 0) {
			return errors.New("unexpected grant cursor result")
		}
		seen := map[string]bool{}
		for _, row := range rows {
			if !validID(row.ID) || !validID(row.UserID) || row.ID == after || seen[row.ID] {
				return errors.New("invalid grant cursor result")
			}
			seen[row.ID] = true
			references = append(references, IdentityUserReference{GrantID: row.ID, UserID: row.UserID})
		}
		more = result.More
		return nil
	})
	if err != nil {
		return nil, false, err
	}
	return references, more, nil
}
