package gateways

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

var ErrLastOwner = errors.New("the last Gateway owner cannot be removed")

type GrantRequest struct {
	RoleID    string `json:"role_id"`
	UserID    string `json:"user_id"`
	GatewayID string `json:"gateway_id"`
	Scope     string `json:"scope"`
}

// CreateGrant commits a Gateway grant and its events under the Gateway lock.
func (s *Service) CreateGrant(ctx context.Context, p Principal, input GrantRequest) (model.RoleBinding, error) {
	var grant model.RoleBinding
	if err := validatePrincipal(p); err != nil {
		return grant, err
	}
	if input.Scope != "gateway" || !validID(input.GatewayID) || !validID(input.UserID) || !validID(input.RoleID) {
		return grant, ErrInvalid
	}
	err := s.repository.WithLockedResource(ctx, "Gateway", "id", input.GatewayID, func(ctx context.Context, tx store.Transaction, _ any) error {
		if _, err := s.mutationTarget(ctx, tx, p, input.GatewayID, false); err != nil {
			return err
		}
		value, err := tx.Get(ctx, "Role", input.RoleID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		role, ok := value.(model.Role)
		if !ok {
			return errors.New("unexpected role storage result")
		}
		if role.Name != "gateway:owner" && role.Name != "gateway:viewer" {
			return ErrForbidden
		}
		value, err = tx.Get(ctx, "User", input.UserID)
		if errors.Is(err, store.ErrNotFound) {
			return ErrInvalid
		}
		if err != nil {
			return err
		}
		user, ok := value.(model.User)
		if !ok {
			return errors.New("unexpected user storage result")
		}
		if user.Issuer == nil || user.Subject == nil || *user.Issuer == "" || *user.Subject == "" {
			return ErrInvalid
		}
		id, err := ksuid.NewRandom()
		if err != nil {
			return err
		}
		grant = model.RoleBinding{Meta: model.Meta{ID: id.String()}, UserID: input.UserID, RoleID: input.RoleID, GatewayID: input.GatewayID, Scope: input.Scope}
		if err := tx.Create(ctx, "RoleBinding", grant); err != nil {
			return err
		}
		value, err = tx.Get(ctx, "RoleBinding", grant.ID)
		if err != nil {
			return err
		}
		grant, ok = value.(model.RoleBinding)
		if !ok {
			return errors.New("unexpected grant storage result")
		}
		return notifyGrant(tx, grant, "Create", "rolebinding.created")
	})
	if err != nil {
		return model.RoleBinding{}, err
	}
	return grant, nil
}

// GetGrant permits the grantee, a Gateway owner, or a configured control plane.
func (s *Service) GetGrant(ctx context.Context, p Principal, id string) (model.RoleBinding, error) {
	var grant model.RoleBinding
	if err := validatePrincipal(p); err != nil {
		return grant, err
	}
	if !validID(id) {
		return grant, store.ErrNotFound
	}
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		value, err := tx.Get(ctx, "RoleBinding", id)
		if err != nil {
			return err
		}
		var ok bool
		grant, ok = value.(model.RoleBinding)
		if !ok {
			return errors.New("unexpected grant storage result")
		}
		if _, err := tx.Get(ctx, "Gateway", grant.GatewayID); err != nil {
			return err
		}
		user, err := syncUser(ctx, tx, p)
		if err != nil {
			return err
		}
		if grant.UserID == user.ID {
			return nil
		}
		_, err = s.mutationTarget(ctx, tx, p, grant.GatewayID, false)
		return err
	})
	if err != nil {
		return model.RoleBinding{}, err
	}
	return grant, nil
}

func (s *Service) DeleteGrant(ctx context.Context, p Principal, id string) error {
	if err := validatePrincipal(p); err != nil {
		return err
	}
	if !validID(id) {
		return store.ErrNotFound
	}
	// This read selects the lock key only. The transaction repeats every check.
	value, err := s.repository.Get(ctx, "RoleBinding", id)
	if err != nil {
		return err
	}
	initial, ok := value.(model.RoleBinding)
	if !ok {
		return errors.New("unexpected grant storage result")
	}
	return s.repository.WithLockedResource(ctx, "Gateway", "id", initial.GatewayID, func(ctx context.Context, tx store.Transaction, _ any) error {
		if _, err := s.mutationTarget(ctx, tx, p, initial.GatewayID, false); err != nil {
			return err
		}
		value, err := tx.Get(ctx, "RoleBinding", id)
		if err != nil {
			return err
		}
		grant, ok := value.(model.RoleBinding)
		if !ok || grant.GatewayID != initial.GatewayID {
			return errors.New("grant changed its Gateway")
		}
		owner, err := findRole(ctx, tx, "gateway:owner")
		if err != nil {
			return err
		}
		if grant.RoleID == owner.ID {
			result, err := tx.List(ctx, "RoleBinding", "gateway_id", grant.GatewayID, store.ListOptions{Page: 1, CountOnly: true, ImplicitFilters: map[string]string{"role_id": owner.ID, "scope": "gateway"}})
			if err != nil {
				return err
			}
			if result.Total <= 1 {
				return ErrLastOwner
			}
		}
		if err := tx.Delete(ctx, "RoleBinding", id); err != nil {
			return err
		}
		return notifyGrant(tx, grant, "Delete", "rolebinding.deleted")
	})
}
func notifyGrant(tx store.Transaction, grant model.RoleBinding, eventType, kind string) error {
	payload, err := json.Marshal(map[string]string{"source": "RoleBindings", "source_id": grant.ID, "event_type": eventType, "gateway_id": grant.GatewayID})
	if err != nil {
		return err
	}
	id, err := uuid.NewRandom()
	if err != nil {
		return err
	}
	if err := tx.Notify(store.Notification{ID: id, Destination: "kafka", ResourceKey: grant.ID, Kind: kind, Payload: payload}); err != nil {
		return err
	}
	return notifyGateway(tx, grant.GatewayID, "Update", "gateway.updated")
}
