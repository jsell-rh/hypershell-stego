package serviceaccounts

import (
	"context"
	"encoding/hex"
	"errors"
	"strconv"
	"strings"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

var errInventoryPending = errors.New("provider inventory still contains an owned client")

func validInventoryProviderID(id string) bool {
	return len(id) > 0 && len(id) <= 255 && strings.IndexFunc(id, func(r rune) bool {
		return !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '-')
	}) < 0
}
func (s *Service) recoverGatewayInventory(ctx context.Context, gateway model.Gateway) (bool, error) {
	query, cancel := context.WithTimeout(ctx, time.Second)
	version, err := s.provider.InventorySource(query, gateway.ID)
	err = errors.Join(err, query.Err())
	cancel()
	if err != nil {
		return false, err
	}
	decoded, err := hex.DecodeString(version)
	if err != nil || len(decoded) != 32 || hex.EncodeToString(decoded) != version {
		return false, runtime.ErrScanContract
	}
	sourceVersion := strconv.FormatInt(gateway.ResourceGeneration, 10) + ":" + version + ":inventory-v1"
	access, err := s.cleanupCheckpointAccess(gateway, "gateway-provider-inventory", sourceVersion)
	if err != nil {
		return false, err
	}
	progress, err := scanGatewayCleanup(ctx, sourceVersion, access, func(ctx context.Context, after string, limit int) (runtime.CursorPage[string], error) {
		page, err := s.provider.InventoryPage(ctx, gateway.ID, version, after, limit)
		if err != nil {
			return page, err
		}
		for _, item := range page.Items {
			if !validInventoryProviderID(item.Value) {
				return runtime.CursorPage[string]{}, runtime.ErrScanContract
			}
		}
		return page, nil
	}, func(ctx context.Context, providerID string) error {
		call, stop := context.WithTimeout(ctx, 750*time.Millisecond)
		defer stop()
		owned, err := s.provider.PrepareInventoryCandidate(call, gateway.ID, version, providerID)
		err = errors.Join(err, call.Err())
		if err != nil {
			return err
		}
		// Found clients make this cycle incomplete for cleanup. Their saved journals
		// are deleted by retained recovery. A later clean cycle must find none.
		if owned {
			return errInventoryPending
		}
		return nil
	}, 20, s.cleanupWorkers, func(providerID string) string { return providerID })
	return err == nil && progress.Complete, err
}
