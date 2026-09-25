package acceptance

import (
	"context"
	"errors"
	"testing"

	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// The generated store guards every write transaction with the monotonic
// database epoch and the bootstrap identity. A database restored from an
// earlier backup loses committed epoch values; the store must reject the next
// write and stay closed. The lease is off in this deployment, so the epoch and
// identity checks are the only restore detection.
func TestEpochRollbackOnRestoredDatabaseIsRejected(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
			return f.storage.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: ksuid.New().String()}, Name: "release", Image: "registry.example/gateway:v1"})
		}); err != nil {
			t.Fatal(err)
		}
	}
	// The runtime role holds USAGE on the epoch sequence, so the store cannot
	// read last_value directly. The operator connection observes the sequence.
	var high int64
	if err := f.db.QueryRow("SELECT last_value FROM stego_schema.epoch_seq").Scan(&high); err != nil || high < 2 {
		t.Fatal("epoch did not advance over two writes", high, err)
	}
	// Simulate a restore of an earlier backup: reset the epoch sequence to a
	// lower value while the marker and the identity stay intact.
	if _, err := f.db.Exec("ALTER SEQUENCE stego_schema.epoch_seq RESTART WITH 1"); err != nil {
		t.Fatal(err)
	}
	if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		return f.storage.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: ksuid.New().String()}, Name: "release", Image: "registry.example/gateway:v1"})
	}); !errors.Is(err, model.ErrDatabaseRollback) {
		t.Fatal("write to a restored database was accepted", err)
	}
	// The latch stays closed after the first detection.
	if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		return f.storage.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: ksuid.New().String()}, Name: "release", Image: "registry.example/gateway:v1"})
	}); !errors.Is(err, model.ErrDatabaseRollback) {
		t.Fatal("store served a later write after rollback detection", err)
	}
	if _, err := f.storage.DatabaseIdentity(); !errors.Is(err, model.ErrDatabaseRollback) {
		t.Fatal("identity read after rollback detection", err)
	}
	if _, err := f.storage.DatabaseEpoch(); !errors.Is(err, model.ErrDatabaseRollback) {
		t.Fatal("epoch read after rollback detection", err)
	}
}

// A replaced database is a backup of a different instance: the epoch sequence
// moves forward but the bootstrap identity belongs to another database. The
// store must reject the write even though the epoch alone looks healthy.
func TestEpochRollbackOnReplacedDatabaseIdentityIsRejected(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	if _, err := f.storage.DatabaseIdentity(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("ALTER SEQUENCE stego_schema.epoch_seq RESTART WITH 1000"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("UPDATE stego_schema.identity SET database_id='replaced' WHERE singleton"); err != nil {
		t.Fatal(err)
	}
	if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		return f.storage.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: ksuid.New().String()}, Name: "release", Image: "registry.example/gateway:v1"})
	}); !errors.Is(err, model.ErrDatabaseRollback) {
		t.Fatal("write to a replaced database was accepted", err)
	}
}

// The lease is off in this deployment. Bootstrap must not create the lease
// table, no lease error exists in the generated API, and independent stores
// write without fencing while the epoch and identity checks stay active.
func TestWriterLeaseOffDeploymentRunsUnfenced(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	var leaseTable bool
	if err := f.db.QueryRow("SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_tables WHERE schemaname='stego_schema' AND tablename='writer_lease')").Scan(&leaseTable); err != nil || leaseTable {
		t.Fatal("writer_lease table exists with the lease off", err)
	}
	if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		return f.storage.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: ksuid.New().String()}, Name: "one", Image: "registry.example/gateway:v1"})
	}); err != nil {
		t.Fatal(err)
	}
	// An independent store over the same runtime role writes while the first
	// store stays live: the lease does not fence it.
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: f.runtime}), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	second, err := model.NewStore(orm)
	if err != nil {
		t.Fatal(err)
	}
	if err := second.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		return second.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: ksuid.New().String()}, Name: "two", Image: "registry.example/gateway:v1"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := f.storage.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		return f.storage.Create(ctx, "GatewayRelease", model.GatewayRelease{Meta: model.Meta{ID: ksuid.New().String()}, Name: "three", Image: "registry.example/gateway:v1"})
	}); err != nil {
		t.Fatal("first store was fenced with the lease off", err)
	}
}
