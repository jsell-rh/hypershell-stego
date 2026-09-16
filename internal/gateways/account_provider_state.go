package gateways

import (
	"context"
	"errors"
	"math"

	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// AccountProviderStateScope binds a journal to its Gateway as well as its
// account ID. These keys are fixed; domain policy stays in the account service.
func AccountProviderStateScope(gatewayID string) string { return "gateway:" + gatewayID }

func (s *Service) authorizeAccountProviderState(p Principal) error {
	// The common grant check validates issuer and subject. Machine callers do
	// not need user profile fields, and this path does not create a user row.
	if !s.isControlPlane(p) || !s.providerStatePolicy.Allows(auth.Identity{Issuer: p.Issuer, UserID: p.Subject}, "ServiceAccount", "provider-state", "") {
		return ErrForbidden
	}
	return nil
}

// checkAccountProviderState uses snapshot reads. The caller of the provisioner
// can hold the Gateway row lock while a separate RPC commits this journal.
// This path must not acquire that lock or write any domain row. Recovery state
// does not authorize a provider action; the private provisioner caller does.
func checkAccountProviderState(ctx context.Context, tx store.Transaction, gatewayID, accountID string, cleanup bool) error {
	reader, ok := tx.(store.RetainedReader)
	if !ok {
		return errors.New("account provider state requires retained storage")
	}
	value, err := reader.GetRetained(ctx, "Gateway", gatewayID)
	if err != nil {
		return err
	}
	gateway, ok := value.(model.Gateway)
	if !ok || gateway.ID != gatewayID || gateway.ResourceVersion < 1 {
		return errors.New("account provider Gateway does not match")
	}
	if !cleanup && gateway.DeletedAt.Valid {
		return store.ErrVersionConflict
	}
	value, err = reader.GetRetained(ctx, "ServiceAccount", accountID)
	if errors.Is(err, store.ErrNotFound) && cleanup {
		return nil
	}
	if err != nil {
		return err
	}
	account, ok := value.(model.ServiceAccount)
	if !ok || account.ID != accountID || account.GatewayID != gatewayID || account.ClientID != "hs-sa-"+gatewayID+"-"+accountID {
		return ErrForbidden
	}
	if !cleanup {
		if account.DeletedAt.Valid {
			return store.ErrVersionConflict
		}
		switch account.Status {
		case "provisioning", "ready", "degraded":
		default:
			return store.ErrVersionConflict
		}
	}
	return nil
}

func (s *Service) LoadAccountProviderState(ctx context.Context, p Principal, gatewayID, accountID string, cleanup bool) (result store.ResourceState, err error) {
	if !validID(gatewayID) || !validID(accountID) {
		return result, ErrInvalid
	}
	if err = s.authorizeAccountProviderState(p); err != nil {
		return result, err
	}
	err = s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		if err := checkAccountProviderState(ctx, tx, gatewayID, accountID, cleanup); err != nil {
			return err
		}
		records, ok := tx.(store.ResourceStateStore)
		if !ok {
			return errors.New("account provider state storage is required")
		}
		result, err = records.LoadResourceState(ctx, "ServiceAccount", accountID, AccountProviderStateScope(gatewayID))
		if err != nil {
			return err
		}
		if result.Version > 0 && (len(result.Data) > MaxGatewayProviderStateBytes || runtime.CheckStateEnvelope(result.Data) != nil) {
			return store.ErrResourceState
		}
		return nil
	})
	if err != nil {
		return store.ResourceState{}, err
	}
	return result, nil
}

// SaveAccountProviderState commits one ciphertext version without changing
// account status, role, expiry, grants, or events. Cleanup permits orphan state
// only under a retained Gateway. The exact private grant is required in all cases.
func (s *Service) SaveAccountProviderState(ctx context.Context, p Principal, gatewayID, accountID string, cleanup bool, expected int64, sealed []byte) (result store.ResourceState, err error) {
	if !validID(gatewayID) || !validID(accountID) || expected < 0 || expected == math.MaxInt64 || len(sealed) > MaxGatewayProviderStateBytes || runtime.CheckStateEnvelope(sealed) != nil {
		return result, ErrInvalid
	}
	if err = s.authorizeAccountProviderState(p); err != nil {
		return result, err
	}
	err = s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		if err := checkAccountProviderState(ctx, tx, gatewayID, accountID, cleanup); err != nil {
			return err
		}
		records, ok := tx.(store.ResourceStateStore)
		if !ok {
			return errors.New("account provider state storage is required")
		}
		result, err = records.SaveResourceState(ctx, "ServiceAccount", accountID, AccountProviderStateScope(gatewayID), expected, sealed)
		return err
	})
	if err != nil {
		return store.ResourceState{}, err
	}
	return result, nil
}
