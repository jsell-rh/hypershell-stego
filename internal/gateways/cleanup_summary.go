package gateways

import (
	"context"
	"errors"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
)

func (s *Service) CleanupSummary(ctx context.Context, p Principal, owner, target string) (store.CleanupSummary, error) {
	var result store.CleanupSummary
	if err := validatePrincipal(p); err != nil {
		return result, err
	}
	if !s.isControlPlane(p) || (owner != "identity" && owner != "workload") || (owner == "identity" && target != "") || (owner == "workload" && !validID(target)) {
		return result, ErrForbidden
	}
	if err := s.AuthorizeCleanup(p, "Gateway", owner, target); err != nil {
		return result, err
	}
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		reader, ok := tx.(store.CleanupSummaryReader)
		if !ok {
			return errors.New("Gateway storage has no cleanup summary reader")
		}
		var err error
		result, err = reader.ReadCleanupSummary(ctx, "Gateway", owner, target, "", "")
		return err
	})
	return result, err
}
