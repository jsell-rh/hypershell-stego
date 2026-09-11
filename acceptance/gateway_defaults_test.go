package acceptance

import (
	"context"
	"errors"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

func TestGatewayDefaultsRemainExplicitAndAtomic(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	creator := principal("default-owner", "gateway:creator")
	request := f.request("default-placement")
	request.ClusterID = ""
	request.ReleaseID = ""
	if _, err := f.service.Create(ctx, creator, request); !errors.Is(err, gateways.ErrInvalid) {
		t.Fatal("unset defaults selected placement", err)
	}
	service, err := gateways.New(f.storage, gateways.Options{DatabaseProvider: gateways.ProviderCNPG, DefaultClusterID: f.cluster, DefaultReleaseID: f.release})
	if err != nil {
		t.Fatal(err)
	}
	// More than one release must not change the operator's selection.
	other := ksuid.New().String()
	if err = f.storage.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: other}, Name: "other", Image: "registry.example/other:v1"}); err != nil {
		t.Fatal(err)
	}
	row, err := service.Create(ctx, creator, request)
	if err != nil || row.ClusterID != f.cluster || row.ReleaseID != f.release || row.DatabaseID != f.database {
		t.Fatal("default placement differs", err)
	}
	request.Name = "explicit-release"
	request.ReleaseID = other
	row, err = service.Create(ctx, creator, request)
	if err != nil || row.ReleaseID != other {
		t.Fatal("default replaced an explicit release", err)
	}
	broken, err := gateways.New(f.storage, gateways.Options{DatabaseProvider: gateways.ProviderCNPG, DefaultClusterID: f.cluster, DefaultReleaseID: ksuid.New().String()})
	if err != nil {
		t.Fatal(err)
	}
	before := map[string]int{}
	for _, table := range []string{"gateways", "users", "role_bindings", "stego_outbox.messages"} {
		before[table] = count(t, f.db, table)
	}
	request.Name = "missing-default"
	request.ReleaseID = ""
	if _, err = broken.Create(ctx, principal("missing-default-owner", "gateway:creator"), request); !errors.Is(err, gateways.ErrInvalid) {
		t.Fatal("missing default release accepted", err)
	}
	for table, total := range before {
		if count(t, f.db, table) != total {
			t.Fatal("failed default wrote", table)
		}
	}
	if _, err = service.Create(ctx, principal("denied-default-owner"), request); !errors.Is(err, gateways.ErrForbidden) {
		t.Fatal("defaults bypassed creation permission", err)
	}
}
