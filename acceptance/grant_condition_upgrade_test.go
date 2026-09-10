package acceptance

import (
	"context"
	_ "embed"
	"os"
	"testing"

	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

// The deployed condition contract from application 0128ef569be258a92906cf6da0ab6d32625b54f4.
//
//go:embed testdata/pre_grant_conditions.sql
var preGrantConditions string

func TestGrantConditionUpgradePreservesClientHistory(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("condition-upgrade"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, preGrantConditions); err != nil {
		t.Fatal(err)
	}
	read := func() model.Gateway {
		t.Helper()
		value, err := f.storage.Get(ctx, "Gateway", gateway.ID)
		if err != nil {
			t.Fatal(err)
		}
		return value.(model.Gateway)
	}
	before := read()
	if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		return tx.(store.ConditionWriter).ObserveConditionsIfVersion(ctx, "Gateway", gateway.ID, before.ResourceVersion, "identity", []store.ConditionUpdate{{Name: "ClientReady", Status: "True", Reason: "IdentityClientReady", Message: "The Gateway identity client is configured"}})
	}); err != nil {
		t.Fatal(err)
	}
	before = read()
	old, err := before.Conditions()
	if err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(postgres.New(postgres.Config{Conn: f.db}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := model.NewStore(db); err == nil {
		t.Fatal("new store accepted the previous condition contract")
	}
	migration, err := os.ReadFile("../out/storage/migrations/000007_resource_conditions.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	if _, err := model.NewStore(db); err != nil {
		t.Fatal("upgraded store was rejected", err)
	}
	after := read()
	values, err := after.Conditions()
	if err != nil {
		t.Fatal(err)
	}
	client := values["identity"]["ClientReady"]
	grants := values["identity_users"]["GrantsSynchronized"]
	if after.ResourceGeneration != before.ResourceGeneration+1 || after.ResourceVersion != before.ResourceVersion+1 || client.Current || client.Status != "True" || !client.LastTransitionTime.Equal(old["identity"]["ClientReady"].LastTransitionTime) || grants.Current || grants.Status != "Unknown" {
		t.Fatal("upgrade changed history or retained current evidence", before, after, values)
	}
	if _, err := f.db.ExecContext(ctx, string(migration)); err != nil {
		t.Fatal(err)
	}
	stable := read()
	if stable.ResourceGeneration != after.ResourceGeneration || stable.ResourceVersion != after.ResourceVersion {
		t.Fatal("repeated migration invalidated evidence again")
	}
}
