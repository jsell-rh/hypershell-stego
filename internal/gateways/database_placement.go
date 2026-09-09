package gateways

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"

	"github.com/jsell-rh/hypershell-stego/internal/resourceevents"
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

// placeDatabase runs inside the Gateway creation transaction. A public request
// cannot select an existing deployment database or change the provider setting.
func (s *Service) placeDatabase(ctx context.Context, tx store.Transaction, gatewayName string) (string, error) {
	switch s.databaseProvider {
	case ProviderCNPG:
		result, err := tx.List(ctx, "ManagedDatabase", "", "", store.ListOptions{Page: 1, Size: 2})
		if err != nil {
			return "", err
		}
		rows, ok := result.Items.([]model.ManagedDatabase)
		if !ok {
			return "", errors.New("unexpected database storage result")
		}
		if result.Total != 1 || len(rows) != 1 || rows[0].Provider != ProviderCNPG {
			return "", fmt.Errorf("%w: CNPG placement requires one managed database with provider cnpg", ErrInvalid)
		}
		return rows[0].ID, nil
	case ProviderDeployment:
		key, err := ksuid.NewRandom()
		if err != nil {
			return "", err
		}
		id := key.String()
		namespace, err := DatabaseNamespace(id)
		if err != nil {
			return "", err
		}
		row := model.ManagedDatabase{Meta: model.Meta{ID: id}, Name: "gw-" + gatewayName + "-db", Provider: ProviderDeployment, Namespace: namespace}
		if err := tx.Create(ctx, "ManagedDatabase", row); err != nil {
			return "", err
		}
		if err := resourceevents.Notify(tx, "ManagedDatabases", id, "Create", "manageddatabase.created"); err != nil {
			return "", err
		}
		return id, nil
	default:
		return "", errors.New("database placement is not configured")
	}
}
