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
		for {
			page, err := readGrantPage(ctx, tx, opts)
			if err != nil {
				return err
			}
			if page.Total > 10000 {
				return ErrGrantCapacity
			}
			rows = append(rows, page.Items...)
			if int64(len(rows)) >= page.Total {
				return nil
			}
			if len(page.Items) == 0 {
				return errors.New("grant snapshot is incomplete")
			}
			opts.Page++
		}
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
	page := GrantPage{Total: result.Total, Items: make([]GrantView, 0, len(rows))}
	if len(rows) == 0 {
		return page, nil
	}
	roleIDs, userIDs := []string{}, []string{}
	for _, row := range rows {
		if !slices.Contains(roleIDs, row.RoleID) {
			roleIDs = append(roleIDs, row.RoleID)
		}
		if !slices.Contains(userIDs, row.UserID) {
			userIDs = append(userIDs, row.UserID)
		}
	}
	lookup := func(entity string, ids []string) (store.ListResult, error) {
		return tx.List(ctx, entity, "", "", store.ListOptions{Page: 1, Size: 100, Filter: &store.RowFilter{Field: "id", Values: ids}})
	}
	roles, err := lookup("Role", roleIDs)
	if err != nil {
		return GrantPage{}, err
	}
	users, err := lookup("User", userIDs)
	if err != nil {
		return GrantPage{}, err
	}
	roleRows, ok := roles.Items.([]model.Role)
	if !ok {
		return GrantPage{}, errors.New("unexpected role storage result")
	}
	userRows, ok := users.Items.([]model.User)
	if !ok {
		return GrantPage{}, errors.New("unexpected user storage result")
	}
	roleNames, usernames := map[string]string{}, map[string]string{}
	for _, row := range roleRows {
		roleNames[row.ID] = row.Name
	}
	for _, row := range userRows {
		usernames[row.ID] = row.Username
	}
	for _, row := range rows {
		// Consumers must not treat an unresolved role as an absent grant.
		if roleNames[row.RoleID] == "" {
			return GrantPage{}, errors.New("grant role cannot be resolved")
		}
		page.Items = append(page.Items, GrantView{Grant: row, RoleName: roleNames[row.RoleID], Username: usernames[row.UserID]})
	}
	return page, nil
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
