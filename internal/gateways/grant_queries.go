package gateways

import (
	"context"
	"errors"
	"slices"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

var ErrGrantCapacity = errors.New("grant response exceeds its resource limit")

type GrantQuery struct {
	Page, Size        int
	Search            string
	OrderBy           []store.OrderByField
	UserID, GatewayID string
}
type GrantView struct {
	Grant              model.RoleBinding
	RoleName, Username string
}
type GrantPage struct {
	Items []GrantView
	Total int64
}

func (s *Service) grantOptions(ctx context.Context, tx store.Transaction, p Principal, q GrantQuery) (store.ListOptions, error) {
	user, err := syncUser(ctx, tx, p)
	if err != nil {
		return store.ListOptions{}, err
	}
	conditions := []store.RowFilter{{Related: &store.RelatedFilter{Entity: "Gateway", LocalField: "gateway_id", ForeignField: "id"}}}
	if !s.isControlPlane(p) {
		owner, err := findRole(ctx, tx, "gateway:owner")
		if err != nil {
			return store.ListOptions{}, err
		}
		conditions = append(conditions, store.RowFilter{Any: []store.RowFilter{
			{Field: "user_id", Values: []string{user.ID}},
			{Related: &store.RelatedFilter{Entity: "RoleBinding", LocalField: "gateway_id", ForeignField: "gateway_id", Values: map[string][]string{"user_id": {user.ID}, "role_id": {owner.ID}, "scope": {"gateway"}}}},
		}})
	}
	ordering := append([]store.OrderByField(nil), q.OrderBy...)
	if !slices.ContainsFunc(ordering, func(v store.OrderByField) bool { return v.Field == "id" }) {
		ordering = append(ordering, store.OrderByField{Field: "id", Direction: "asc"})
	}
	opts := store.ListOptions{Page: q.Page, Size: q.Size, CountOnly: q.Size == 0, Search: q.Search, OrderBy: ordering, Filter: &store.RowFilter{All: conditions}, ImplicitFilters: map[string]string{"scope": "gateway"}}
	if q.UserID != "" {
		opts.ImplicitFilters["user_id"] = q.UserID
	}
	if q.GatewayID != "" {
		opts.ImplicitFilters["gateway_id"] = q.GatewayID
	}
	return opts, nil
}
func validGrantQuery(q GrantQuery) bool {
	return q.Page >= 1 && q.Page <= 1000000 && q.Size >= 0 && q.Size <= 100 && (q.UserID == "" || validID(q.UserID)) && (q.GatewayID == "" || validID(q.GatewayID))
}

// ListGrants applies access before search, counts, and paging in one transaction.
func (s *Service) ListGrants(ctx context.Context, p Principal, q GrantQuery) (GrantPage, error) {
	if err := validatePrincipal(p); err != nil {
		return GrantPage{}, err
	}
	if !validGrantQuery(q) {
		return GrantPage{}, ErrInvalid
	}
	var page GrantPage
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		opts, err := s.grantOptions(ctx, tx, p, q)
		if err != nil {
			return err
		}
		page, err = readGrantPage(ctx, tx, opts)
		return err
	})
	if err != nil {
		return GrantPage{}, err
	}
	return page, nil
}

// AllGrants serves the unpaged reference API and its initial watch replay.
// It returns a complete snapshot or an error. It never returns a partial list.
func (s *Service) AllGrants(ctx context.Context, p Principal, userID, gatewayID string) ([]GrantView, error) {
	if err := validatePrincipal(p); err != nil {
		return nil, err
	}
	q := GrantQuery{Page: 1, Size: 100, UserID: userID, GatewayID: gatewayID}
	if !validGrantQuery(q) {
		return nil, ErrInvalid
	}
	rows := make([]GrantView, 0)
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		opts, err := s.grantOptions(ctx, tx, p, q)
		if err != nil {
			return err
		}
		opts.Size = 10000
		result, err := tx.List(ctx, "RoleBinding", "", "", opts)
		if err != nil {
			return err
		}
		if result.Total > 10000 {
			return ErrGrantCapacity
		}
		grants, ok := result.Items.([]model.RoleBinding)
		if !ok || int64(len(grants)) != result.Total {
			return errors.New("grant snapshot is incomplete")
		}
		rows, err = projectGrants(ctx, tx, grants)
		return err
	})
	if err != nil {
		return nil, err
	}
	return rows, nil
}

func readGrantPage(ctx context.Context, tx store.Transaction, opts store.ListOptions) (GrantPage, error) {
	result, err := tx.List(ctx, "RoleBinding", "", "", opts)
	if err != nil {
		return GrantPage{}, err
	}
	rows, ok := result.Items.([]model.RoleBinding)
	if !ok {
		return GrantPage{}, errors.New("unexpected grant storage result")
	}
	views, err := projectGrants(ctx, tx, rows)
	if err != nil {
		return GrantPage{}, err
	}
	return GrantPage{Total: result.Total, Items: views}, nil
}

// projectGrants reads each distinct role and user once per snapshot. Each
// lookup remains bounded to 100 IDs and uses the caller's transaction.
func projectGrants(ctx context.Context, tx store.Transaction, rows []model.RoleBinding) ([]GrantView, error) {
	views := make([]GrantView, 0, len(rows))
	if len(rows) > 10000 {
		return nil, ErrGrantCapacity
	}
	roleNames, usernames := map[string]string{}, map[string]string{}
	lookup := func(entity string, ids []string) (store.ListResult, error) {
		fields := []string{"id", "name"}
		if entity == "User" {
			fields = []string{"id", "username"}
		}
		return tx.List(ctx, entity, "", "", store.ListOptions{Page: 1, Size: 100, Fields: fields, Filter: &store.RowFilter{Field: "id", Values: ids}})
	}
	for start := 0; start < len(rows); start += 100 {
		batch := rows[start:min(start+100, len(rows))]
		roleIDs, userIDs := []string{}, []string{}
		for _, row := range batch {
			if _, seen := roleNames[row.RoleID]; !seen {
				roleIDs = append(roleIDs, row.RoleID)
				roleNames[row.RoleID] = ""
			}
			if _, seen := usernames[row.UserID]; !seen {
				userIDs = append(userIDs, row.UserID)
				usernames[row.UserID] = ""
			}
		}
		if len(roleIDs) > 0 {
			result, err := lookup("Role", roleIDs)
			if err != nil {
				return nil, err
			}
			roleRows, ok := result.Items.([]model.Role)
			if !ok {
				return nil, errors.New("unexpected role storage result")
			}
			for _, row := range roleRows {
				roleNames[row.ID] = row.Name
			}
		}
		if len(userIDs) > 0 {
			result, err := lookup("User", userIDs)
			if err != nil {
				return nil, err
			}
			userRows, ok := result.Items.([]model.User)
			if !ok {
				return nil, errors.New("unexpected user storage result")
			}
			for _, row := range userRows {
				usernames[row.ID] = row.Username
			}
		}
		for _, row := range batch {
			// Consumers must not treat an unresolved role as an absent grant.
			if roleNames[row.RoleID] == "" {
				return nil, errors.New("grant role cannot be resolved")
			}
			views = append(views, GrantView{Grant: row, RoleName: roleNames[row.RoleID], Username: usernames[row.UserID]})
		}
	}
	return views, nil
}

// EventGrant checks current access and the stored deletion state.
func (s *Service) EventGrant(ctx context.Context, p Principal, id string, deleted bool) (GrantView, error) {
	if err := validatePrincipal(p); err != nil {
		return GrantView{}, err
	}
	if !validID(id) {
		return GrantView{}, store.ErrNotFound
	}
	var view GrantView
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		opts, err := s.grantOptions(ctx, tx, p, GrantQuery{Page: 1, Size: 1})
		if err != nil {
			return err
		}
		opts.IncludeDeleted = deleted
		opts.ImplicitFilters["id"] = id
		page, err := readGrantPage(ctx, tx, opts)
		if err != nil {
			return err
		}
		if len(page.Items) != 1 || page.Items[0].Grant.DeletedAt.Valid != deleted {
			return store.ErrNotFound
		}
		view = page.Items[0]
		return nil
	})
	if err != nil {
		return GrantView{}, err
	}
	return view, nil
}
