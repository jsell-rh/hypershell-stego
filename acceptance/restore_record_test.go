package acceptance

import (
	"context"
	"errors"
	"testing"

	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

// The generated store records the restore point of a backup: the schema
// release and the database identity and epoch of the data. An operator
// records it with the backup and verifies the restored database before use.
// The whole record path must work under the limited runtime role, and
// verification must read only.
func TestRestoreRecordVerificationThroughRuntimeRole(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
			return f.storage.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: ksuid.New().String()}, Name: "release", Image: "registry.example/gateway:v1"})
		}); err != nil {
			t.Fatal(err)
		}
	}
	record, err := f.storage.ReadRestoreRecord(ctx)
	if err != nil {
		t.Fatal("runtime role could not read the restore record", err)
	}
	if record.SchemaGeneration != model.SchemaGeneration || record.SchemaDefinition != model.SchemaDefinition {
		t.Fatal("restore record does not name the running schema release")
	}
	if record.DatabaseIdentity == "" {
		t.Fatal("restore record has no database identity")
	}
	high, err := f.storage.DatabaseEpoch()
	if err != nil || record.DatabaseEpoch != high {
		t.Fatal("restore record epoch does not match the write epoch", record.DatabaseEpoch, high, err)
	}
	if err := f.storage.VerifyRestore(ctx, record); err != nil {
		t.Fatal("verification rejected the live database", err)
	}

	// A record from another schema release or data generation is rejected.
	for name, broken := range map[string]model.RestoreRecord{
		"schema generation": {SchemaGeneration: "other", SchemaDefinition: record.SchemaDefinition, DatabaseIdentity: record.DatabaseIdentity, DatabaseEpoch: record.DatabaseEpoch},
		"schema definition": {SchemaGeneration: record.SchemaGeneration, SchemaDefinition: "other", DatabaseIdentity: record.DatabaseIdentity, DatabaseEpoch: record.DatabaseEpoch},
		"identity":          {SchemaGeneration: record.SchemaGeneration, SchemaDefinition: record.SchemaDefinition, DatabaseIdentity: "other-instance", DatabaseEpoch: record.DatabaseEpoch},
		"epoch":             {SchemaGeneration: record.SchemaGeneration, SchemaDefinition: record.SchemaDefinition, DatabaseIdentity: record.DatabaseIdentity, DatabaseEpoch: record.DatabaseEpoch + 1},
	} {
		if err := f.storage.VerifyRestore(ctx, broken); !errors.Is(err, model.ErrRestore) {
			t.Fatal("verification accepted a record with a different", name, err)
		}
	}

	// Verification is read-only: the epoch does not move and the store keeps
	// writing after any number of failed verifications.
	if _, err := f.storage.DatabaseEpoch(); err != nil || high < 2 {
		t.Fatal("verification advanced the write epoch", high, err)
	}
	if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		return f.storage.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: ksuid.New().String()}, Name: "after-verify", Image: "registry.example/gateway:v1"})
	}); err != nil {
		t.Fatal("store stopped writing after failed verifications", err)
	}
}

// A restored backup is verified against its recorded point, not against the
// process state. An earlier backup replayed under the live schema fails the
// epoch comparison; a backup of another instance fails the identity
// comparison. Both rejections are read-only; the store is not closed.
func TestRestoreVerificationRejectsRestoredDatabases(t *testing.T) {
	for name, tamper := range map[string]func(t testing.TB, f *fixture){
		"earlier backup": func(t testing.TB, f *fixture) {
			if _, err := f.db.Exec("ALTER SEQUENCE stego_schema.epoch_seq RESTART WITH 1"); err != nil {
				t.Fatal(err)
			}
		},
		"other instance": func(t testing.TB, f *fixture) {
			if _, err := f.db.Exec("UPDATE stego_schema.identity SET database_id='replaced' WHERE singleton"); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			f := database(t)
			ctx := context.Background()
			// Advance the epoch with committed writes so the recorded point
			// is above the sequence start.
			for i := 0; i < 2; i++ {
				if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
					return f.storage.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: ksuid.New().String()}, Name: "release", Image: "registry.example/gateway:v1"})
				}); err != nil {
					t.Fatal(err)
				}
			}
			record, err := f.storage.ReadRestoreRecord(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if record.DatabaseEpoch < 2 {
				t.Fatal("fixture writes did not advance the epoch", record.DatabaseEpoch)
			}
			tamper(t, f)
			if err := f.storage.VerifyRestore(ctx, record); !errors.Is(err, model.ErrRestore) {
				t.Fatal("verification accepted a restored database", err)
			}
			// A fresh record from the restored database also mismatches the
			// backup record: the restore cannot verify against its own past.
			if name == "other instance" {
				current, err := f.storage.ReadRestoreRecord(ctx)
				if err != nil {
					t.Fatal(err)
				}
				if current.DatabaseIdentity == record.DatabaseIdentity {
					t.Fatal("restored identity was not replaced")
				}
			}
		})
	}
}
