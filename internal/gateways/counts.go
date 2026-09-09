package gateways

import (
	"context"
	"errors"
	"math"
	"strings"
	"unicode/utf8"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

var ErrCountRange = errors.New("sandbox count exceeds its range")

func (s *Service) AdjustActiveSandboxCount(ctx context.Context, p Principal, namespace string, delta int32) (int32, error) {
	return s.changeCount(ctx, p, namespace, delta, true, "")
}
func (s *Service) SetActiveSandboxCount(ctx context.Context, p Principal, namespace string, count int32) (int32, error) {
	return s.changeCount(ctx, p, namespace, count, false, "")
}

// A count change depends only on the locked Gateway row and the verified
// control-plane identity. Other resource grants do not permit this operation.
func (s *Service) changeCount(ctx context.Context, p Principal, namespace string, input int32, relative bool, cluster string) (int32, error) {
	if err := validatePrincipal(p); err != nil {
		return 0, err
	}
	if !s.isControlPlane(p) {
		return 0, ErrForbidden
	}
	if strings.TrimSpace(namespace) == "" || len(namespace) > 253 || !utf8.ValidString(namespace) || strings.ContainsRune(namespace, 0) {
		return 0, ErrInvalid
	}
	var result int32
	found := false
	err := s.repository.WithLockedResource(ctx, "Gateway", "namespace", namespace, func(ctx context.Context, tx store.Transaction, value any) error {
		found = true
		row, ok := value.(model.Gateway)
		if !ok {
			return errors.New("unexpected Gateway storage result")
		}
		if cluster != "" && row.ClusterID != cluster {
			return ErrPlacementChanged
		}
		next := int64(input)
		if relative && row.ActiveSandboxCount != nil {
			next += int64(*row.ActiveSandboxCount)
		}
		if next < 0 {
			next = 0
		}
		if next > math.MaxInt32 {
			return ErrCountRange
		}
		result = int32(next)
		// NULL becomes zero on the first count operation. A stored equal value
		// requires no resource write and no event.
		if row.ActiveSandboxCount != nil && *row.ActiveSandboxCount == result {
			return nil
		}
		row.ActiveSandboxCount = &result
		if err := tx.Replace(ctx, "Gateway", row.ID, row); err != nil {
			return err
		}
		return notifyGateway(tx, row.ID, "Update", "gateway.updated")
	})
	if !found && errors.Is(err, store.ErrNotFound) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	return result, nil
}

var ErrPlacementChanged = errors.New("Gateway cluster assignment changed")

func (s *Service) SetObservedSandboxCount(ctx context.Context, p Principal, namespace, cluster string, count int32) (int32, error) {
	if !validID(cluster) || count < 0 {
		return 0, ErrInvalid
	}
	return s.changeCount(ctx, p, namespace, count, false, cluster)
}
