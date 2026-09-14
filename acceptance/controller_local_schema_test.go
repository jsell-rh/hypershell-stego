package acceptance

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/schema"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func TestControllerLocalSchemaHasNoDatabaseCatalog(t *testing.T) {
	f := database(t)
	var tables, columns int
	if err := f.db.QueryRow(`SELECT count(*) FROM information_schema.tables WHERE table_schema='public' AND table_name='managed_databases'`).Scan(&tables); err != nil || tables != 0 {
		t.Fatal("database catalog remains", err)
	}
	if err := f.db.QueryRow(`SELECT count(*) FROM information_schema.columns WHERE table_schema='public' AND table_name='gateways' AND column_name='database_id'`).Scan(&columns); err != nil || columns != 0 {
		t.Fatal("Gateway database field remains", err)
	}
	ctx := context.Background()
	creator := principal("controller-local-owner", "gateway:creator")
	gw, err := f.service.Create(ctx, creator, f.request("no-database-registration"))
	if err != nil {
		t.Fatal("Gateway required database registration", err)
	}
	other := ksuid.New().String()
	if err := f.storage.Create(ctx, "ManagedCluster", model.ManagedCluster{Meta: model.Meta{ID: other}, Name: "other", Provider: "kubernetes", KubeconfigSecret: "other"}); err != nil {
		t.Fatal(err)
	}
	events := count(t, f.db, "stego_outbox.messages")
	if _, err := f.service.Update(ctx, creator, gw.ID, gateways.PatchRequest{ClusterID: &other}); !errors.Is(err, store.ErrConflict) {
		t.Fatal("Gateway move accepted", err)
	}
	if events != count(t, f.db, "stego_outbox.messages") {
		t.Fatal("rejected move emitted an event")
	}
	var cluster string
	if err := f.db.QueryRow("SELECT cluster_id FROM gateways WHERE id=$1", gw.ID).Scan(&cluster); err != nil || cluster != f.cluster {
		t.Fatal("rejected move changed placement", err)
	}
}

func TestControllerLocalBootstrapRejectsLegacySchemaWithoutWrites(t *testing.T) {
	for _, populated := range []bool{false, true} {
		name := "empty"
		if populated {
			name = "populated"
		}
		t.Run(name, func(t *testing.T) {
			f := database(t)
			ctx := context.Background()
			gw, err := f.service.Create(ctx, principal("existing", "gateway:creator"), f.request("existing"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(`DROP SCHEMA stego_schema CASCADE; CREATE TABLE managed_databases(id text PRIMARY KEY); ALTER TABLE gateways ADD COLUMN database_id text`); err != nil {
				t.Fatal(err)
			}
			if populated {
				if _, err := f.db.Exec("INSERT INTO managed_databases VALUES ('legacy-server')"); err != nil {
					t.Fatal(err)
				}
			}
			// Prepare an existing connection against the legacy shape before the
			// new application's schema check. Include the retired column explicitly.
			existing, err := f.db.PrepareContext(ctx, "SELECT id,name,database_id FROM gateways WHERE id=$1")
			if err != nil {
				t.Fatal(err)
			}
			defer existing.Close()
			var beforeID, beforeName string
			var beforeDatabase sql.NullString
			if err := existing.QueryRowContext(ctx, gw.ID).Scan(&beforeID, &beforeName, &beforeDatabase); err != nil {
				t.Fatal(err)
			}
			before := map[string]int{}
			for _, table := range []string{"gateways", "managed_databases", "roles", "role_bindings", "stego_outbox.messages"} {
				before[table] = count(t, f.db, table)
			}
			orm, err := gorm.Open(postgres.New(postgres.Config{Conn: f.db}), &gorm.Config{Logger: logger.Discard})
			if err != nil {
				t.Fatal(err)
			}
			if err := schema.Initialize(orm); !errors.Is(err, model.ErrSchemaGeneration) {
				t.Fatal("legacy schema was accepted", err)
			}
			if _, err := model.NewStore(orm); !errors.Is(err, model.ErrSchemaGeneration) {
				t.Fatal("legacy schema started a new store", err)
			}
			for table, total := range before {
				if count(t, f.db, table) != total {
					t.Fatal("rejected bootstrap changed data", table)
				}
			}
			var marked bool
			if err := f.db.QueryRow("SELECT to_regnamespace('stego_schema') IS NOT NULL").Scan(&marked); err != nil || marked {
				t.Fatal("rejected schema received a generation marker", err)
			}
			var afterID, afterName string
			var afterDatabase sql.NullString
			if err := existing.QueryRowContext(ctx, gw.ID).Scan(&afterID, &afterName, &afterDatabase); err != nil || afterID != beforeID || afterName != beforeName || afterDatabase != beforeDatabase {
				t.Fatal("rejection changed an existing connection's legacy read", err)
			}
		})
	}
}
