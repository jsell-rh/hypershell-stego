package catalog

import (
	"errors"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/segmentio/ksuid"
)

func TestDatabaseRegistrationRequiresLocalPlacement(t *testing.T) {
	id, cluster := ksuid.New().String(), ksuid.New().String()
	for _, provider := range []string{"cnpg", "external"} {
		row, err := newDatabase(id, DatabaseCreate{Name: "local", Provider: provider, ClusterID: cluster})
		if err != nil || row.ClusterID == nil || *row.ClusterID != cluster {
			t.Fatal("local database registration failed", err)
		}
		other := ksuid.New().String()
		if err := patchDatabase(&row, DatabasePatch{ClusterID: &other}); !errors.Is(err, gateways.ErrInvalid) || *row.ClusterID != cluster {
			t.Fatal("database placement changed", err)
		}
		if err := patchDatabase(&row, DatabasePatch{ClusterID: &cluster}); err != nil {
			t.Fatal("unchanged placement failed", err)
		}
		// Only an explicit operator value can assign a legacy unplaced record.
		row.ClusterID = nil
		if err := patchDatabase(&row, DatabasePatch{ClusterID: &cluster}); err != nil || row.ClusterID == nil || *row.ClusterID != cluster {
			t.Fatal("explicit legacy placement failed", err)
		}
	}
	for _, input := range []DatabaseCreate{
		{Name: "old", Provider: "deployment", ClusterID: cluster},
		{Name: "missing", Provider: "cnpg"},
		{Name: "invalid", Provider: "external", ClusterID: "not-a-cluster"},
		{Name: "zero", Provider: "cnpg", ClusterID: ksuid.Nil.String()},
	} {
		if _, err := newDatabase(id, input); !errors.Is(err, gateways.ErrInvalid) {
			t.Fatal("invalid registration accepted", err)
		}
	}
}
