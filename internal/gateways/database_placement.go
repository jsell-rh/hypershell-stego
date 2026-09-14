package gateways

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

// DatabaseNamespace derives the immutable namespace from a canonical KSUID.
func DatabaseNamespace(id string) (string, error) {
	if !validID(id) {
		return "", ErrInvalid
	}
	key, _ := ksuid.Parse(id)
	return "openshell-db-" + hex.EncodeToString(key.Payload()[:8]), nil
}

// placeDatabase selects a server registered in the Gateway's managed cluster.
// The request cannot select another Gateway's database or a remote server.
func (s *Service) placeDatabase(ctx context.Context, tx store.Transaction, gatewayName, clusterID string) (string, error) {
	if s.databaseProvider != ProviderCNPG && s.databaseProvider != ProviderExternal {
		return "", errors.New("database placement is not configured")
	}
	result, err := tx.List(ctx, "ManagedDatabase", "cluster_id", clusterID, store.ListOptions{Page: 1, Size: 2})
	if err != nil {
		return "", err
	}
	rows, ok := result.Items.([]model.ManagedDatabase)
	if !ok {
		return "", errors.New("unexpected database storage result")
	}
	if result.Total != 1 || len(rows) != 1 || rows[0].Provider != s.databaseProvider || rows[0].ClusterID == nil || *rows[0].ClusterID != clusterID {
		return "", fmt.Errorf("%w: placement requires one supported database server in the Gateway cluster", ErrInvalid)
	}
	return rows[0].ID, nil
}
