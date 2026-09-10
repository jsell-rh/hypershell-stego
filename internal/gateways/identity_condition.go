package gateways

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// ObserveIdentity commits provider configuration and its condition with one
// event. A condition describes the Gateway identity client, not user grants.
// Provider messages are mapped to fixed text before they reach storage.
func (s *Service) ObserveIdentity(ctx context.Context, p Principal, id string, version int64, oidc *string, reason string) error {
	if err := s.authorizeIdentityController(p, id); err != nil {
		return err
	}
	if version < 1 {
		return ErrObservationRequired
	}
	update := store.ConditionUpdate{Name: "ClientReady", Status: "Unknown", Reason: reason}
	switch reason {
	case "IdentityClientReady":
		if oidc == nil || *oidc == "" {
			return ErrInvalid
		}
		update.Status = "True"
		update.Message = "The Gateway identity client is configured"
	case "IdentityProviderUnavailable":
		update.Message = "The identity provider result could not be confirmed"
	case "IdentityObservationTimeout":
		update.Message = "The identity observation exceeded its time limit"
	default:
		return ErrInvalid
	}
	if update.Status != "True" && oidc != nil {
		return ErrInvalid
	}
	if oidc != nil && (len(*oidc) > 8192 || !utf8.ValidString(*oidc) || strings.ContainsRune(*oidc, 0)) {
		return ErrInvalid
	}
	return s.repository.WithLockedResource(ctx, "Gateway", "id", id, func(ctx context.Context, tx store.Transaction, value any) error {
		row, ok := value.(model.Gateway)
		if !ok || row.ID != id {
			return errors.New("identity resource does not match")
		}
		if row.ResourceVersion != version {
			return store.ErrVersionConflict
		}
		conditions, err := row.Conditions()
		if err != nil {
			return err
		}
		current := conditions["identity"]["ClientReady"]
		changed := oidc != nil && (row.Oidc == nil || *row.Oidc != *oidc)
		if !changed && current.Current && current.Status == update.Status && current.Reason == update.Reason && current.Message == update.Message {
			return nil
		}
		if changed {
			writer, ok := tx.(store.VersionedWriter)
			if !ok {
				return errors.New("identity storage requires conditional writes")
			}
			row.Oidc = oidc
			if err := writer.ReplaceIfVersion(ctx, "Gateway", id, version, row); err != nil {
				return err
			}
			stored, err := tx.Get(ctx, "Gateway", id)
			if err != nil {
				return err
			}
			row, ok = stored.(model.Gateway)
			if !ok {
				return errors.New("identity resource does not match")
			}
			// This transaction holds the row lock. Only the configuration just derived
			// from the validated input changed. No external writer can supply this revision.
			version = row.ResourceVersion
		}
		writer, ok := tx.(store.ConditionWriter)
		if !ok {
			return errors.New("identity storage requires conditions")
		}
		if err := writer.ObserveConditionsIfVersion(ctx, "Gateway", id, version, "identity", []store.ConditionUpdate{update}); err != nil {
			return err
		}
		return notifyGateway(tx, id, "Update", "gateway.updated")
	})
}
