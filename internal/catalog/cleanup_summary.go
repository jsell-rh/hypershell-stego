package catalog

import (
	"context"
	"errors"

	"github.com/jsell-rh/hypershell-stego/internal/databaseplacement"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
)

func (r *Resource[T, C, P]) CleanupSummary(ctx context.Context, p gateways.Principal, owner, provider, cluster string) (store.CleanupSummary, error) {
	var result store.CleanupSummary
	if err := r.authorizeRecovery(p); err != nil {
		return result, err
	}
	if err := r.authorize(p, false); err != nil {
		return result, err
	}
	if r.entity != "ManagedDatabase" || owner != "provider" || (provider != "external" && provider != "cnpg") {
		return result, gateways.ErrInvalid
	}
	target, err := databaseplacement.Target(provider, cluster)
	if err != nil {
		return result, gateways.ErrInvalid
	}
	if err := r.authorizeCleanup(p, r.entity, owner, target); err != nil {
		return result, err
	}
	err = r.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		reader, ok := tx.(store.ScopedCleanupSummaryReader)
		if !ok {
			return errors.New("catalog storage has no cleanup summary reader")
		}
		var err error
		result, err = reader.ReadScopedCleanupSummary(ctx, r.entity, owner, "",
			store.CleanupScope{Field: "cluster_id", Value: cluster},
			store.CleanupScope{Field: "provider", Value: provider})
		return err
	})
	return result, err
}
