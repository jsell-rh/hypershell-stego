package gateways

import (
	"context"
	"errors"
	"slices"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

// PrepareRequest projects the verified claims once at the request boundary.
// These records describe accepted claims. Authorization still uses the current
// token and stored Gateway grants. Watch events must not call this method.
func (s *Service) PrepareRequest(ctx context.Context, p Principal) error {
	if err := validatePrincipal(p); err != nil {
		return err
	}
	return s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		user, err := syncUser(ctx, tx, p)
		if err != nil {
			return err
		}
		roles := make([]model.Role, 0, 2)
		for _, name := range []string{"gateway:creator", "platform:admin"} {
			role, err := findRole(ctx, tx, name)
			if err != nil {
				return err
			}
			roles = append(roles, role)
		}
		result, err := tx.List(ctx, "RoleBinding", "user_id", user.ID, store.ListOptions{Page: 1, Size: 3, ImplicitFilters: map[string]string{"scope": "global"}, Filter: &store.RowFilter{Field: "role_id", Values: []string{roles[0].ID, roles[1].ID}}})
		if err != nil {
			return err
		}
		rows, ok := result.Items.([]model.RoleBinding)
		if !ok || result.Total > 2 || int64(len(rows)) != result.Total {
			return errors.New("global role projection is inconsistent")
		}
		existing := map[string]model.RoleBinding{}
		for _, row := range rows {
			if row.GatewayID != nil {
				return errors.New("global role has a Gateway")
			}
			if _, duplicate := existing[row.RoleID]; duplicate {
				return errors.New("global role is duplicated")
			}
			existing[row.RoleID] = row
		}
		for _, role := range roles {
			row, present := existing[role.ID]
			wanted := slices.Contains(p.Roles, role.Name)
			if wanted == present {
				continue
			}
			if wanted {
				id, err := ksuid.NewRandom()
				if err != nil {
					return err
				}
				row = model.RoleBinding{Meta: model.Meta{ID: id.String()}, RoleID: role.ID, UserID: user.ID, Scope: "global"}
				if err := tx.Create(ctx, "RoleBinding", row); err != nil {
					return err
				}
				if err := notifyGrantChange(tx, row, "Create", "rolebinding.created"); err != nil {
					return err
				}
			} else {
				if err := tx.Delete(ctx, "RoleBinding", row.ID); err != nil {
					return err
				}
				if err := notifyGrantChange(tx, row, "Delete", "rolebinding.deleted"); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
