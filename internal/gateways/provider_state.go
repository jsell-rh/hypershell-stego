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

const identityProviderStateScope = "identity-provider"
const consoleIdentityProviderStateScope = "console-identity-provider"

// Leave space for resource metadata within the generated 64 KiB RPC limit.
const MaxGatewayProviderStateBytes = 60 << 10

type GatewayProviderState struct {
	State           store.ResourceState
	ResourceVersion int64
	Deleted         bool
}

func (s *Service) LoadIdentityProviderState(ctx context.Context, p Principal, id string) (GatewayProviderState, error) {
	return s.loadIdentityProviderState(ctx, p, id, identityProviderStateScope)
}
func (s *Service) LoadConsoleIdentityProviderState(ctx context.Context, p Principal, id string) (GatewayProviderState, error) {
	return s.loadIdentityProviderState(ctx, p, id, consoleIdentityProviderStateScope)
}
func (s *Service) loadIdentityProviderState(ctx context.Context, p Principal, id, scope string) (result GatewayProviderState, err error) {
	if !validID(id) {
		return result, ErrInvalid
	}
	if err = s.authorizeIdentityProviderStateRead(p, id, scope); err != nil {
		return result, err
	}
	err = s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		reader, ok := tx.(store.RetainedReader)
		if !ok {
			return errors.New("provider state requires retained storage")
		}
		value, err := reader.GetRetained(ctx, "Gateway", id)
		if err != nil {
			return err
		}
		row, ok := value.(model.Gateway)
		if !ok || row.ID != id || row.ResourceVersion < 1 {
			return errors.New("provider state resource does not match")
		}
		records, ok := tx.(store.ResourceStateStore)
		if !ok {
			return errors.New("provider state storage is required")
		}
		result.State, err = records.LoadResourceState(ctx, "Gateway", id, scope)
		if err != nil {
			return err
		}
		if result.State.Version > 0 {
			if len(result.State.Data) > MaxGatewayProviderStateBytes {
				return errors.New("provider state exceeds RPC capacity")
			}
			if err := runtime.CheckStateEnvelope(result.State.Data); err != nil {
				return err
			}
		}
		result.ResourceVersion = row.ResourceVersion
		result.Deleted = row.DeletedAt.Valid
		return nil
	})
	return result, err
}

// The trusted provisioner can read only the console record with this grant.
// It receives no native record or state write permission.
func (s *Service) authorizeIdentityProviderStateRead(p Principal, id, scope string) error {
	if scope == consoleIdentityProviderStateScope && validatePrincipal(p) == nil && s.isControlPlane(p) && s.providerStatePolicy.Allows(auth.Identity{Issuer: p.Issuer, UserID: p.Subject}, "Gateway", "read.console-client", "") {
		return nil
	}
	if s.authorizeIdentityController(p, id) == nil {
		return nil
	}
	return s.AuthorizeCleanup(p, "Gateway", "identity", "")
}

// SaveIdentityProviderState keeps recovery writes separate from domain events.
// Live writes hold the resource lock. Cleanup requires irreversible deletion.
// Both paths require the observed resource revision and the record version.
func (s *Service) SaveIdentityProviderState(ctx context.Context, p Principal, id string, resourceVersion, expected int64, sealed []byte, cleanup bool) (GatewayProviderState, error) {
	return s.saveIdentityProviderState(ctx, p, id, identityProviderStateScope, resourceVersion, expected, sealed, cleanup)
}
func (s *Service) SaveConsoleIdentityProviderState(ctx context.Context, p Principal, id string, resourceVersion, expected int64, sealed []byte, cleanup bool) (GatewayProviderState, error) {
	return s.saveIdentityProviderState(ctx, p, id, consoleIdentityProviderStateScope, resourceVersion, expected, sealed, cleanup)
}
func (s *Service) saveIdentityProviderState(ctx context.Context, p Principal, id, scope string, resourceVersion, expected int64, sealed []byte, cleanup bool) (result GatewayProviderState, err error) {
	if !validID(id) || expected < 0 || expected == math.MaxInt64 || len(sealed) > MaxGatewayProviderStateBytes || runtime.CheckStateEnvelope(sealed) != nil {
		return result, ErrInvalid
	}
	if resourceVersion < 1 {
		return result, ErrObservationRequired
	}
	if cleanup {
		err = s.AuthorizeCleanup(p, "Gateway", "identity", "")
	} else {
		err = s.authorizeIdentityController(p, id)
	}
	if err != nil {
		return result, err
	}
	save := func(ctx context.Context, tx store.Transaction, value any) error {
		row, ok := value.(model.Gateway)
		if !ok || row.ID != id {
			return errors.New("provider state resource does not match")
		}
		if row.ResourceVersion != resourceVersion || row.DeletedAt.Valid != cleanup {
			return store.ErrVersionConflict
		}
		records, ok := tx.(store.ResourceStateStore)
		if !ok {
			return errors.New("provider state storage is required")
		}
		result.State, err = records.SaveResourceState(ctx, "Gateway", id, scope, expected, sealed)
		result.ResourceVersion = row.ResourceVersion
		result.Deleted = row.DeletedAt.Valid
		return err
	}
	if !cleanup {
		err = s.repository.WithLockedResource(ctx, "Gateway", "id", id, save)
	} else {
		err = s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
			reader, ok := tx.(store.RetainedReader)
			if !ok {
				return errors.New("provider state requires retained storage")
			}
			value, err := reader.GetRetained(ctx, "Gateway", id)
			if err != nil {
				return err
			}
			return save(ctx, tx, value)
		})
	}
	return result, err
}
