package acceptance

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Keep only query counts. Do not log SQL values or resource contents.
type recoveryQueryLog struct {
	logger.Interface
	counts atomic.Int64
	reads  atomic.Int64
}

func (l *recoveryQueryLog) Trace(_ context.Context, _ time.Time, query func() (string, int64), _ error) {
	sql, _ := query()
	sql = strings.ToLower(strings.TrimSpace(sql))
	if strings.HasPrefix(sql, "select ") {
		l.reads.Add(1)
	}
	if strings.Contains(sql, "count(") {
		l.counts.Add(1)
	}
}

func TestRecoveryPagesAvoidTotals(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	queries := &recoveryQueryLog{Interface: logger.Default.LogMode(logger.Silent)}
	orm, err := gorm.Open(postgres.New(postgres.Config{Conn: f.db}), &gorm.Config{Logger: queries})
	if err != nil {
		t.Fatal(err)
	}
	store, err := model.NewStore(orm)
	if err != nil {
		t.Fatal(err)
	}
	service, err := gateways.New(store, gateways.Options{ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		t.Fatal(err)
	}
	resources, err := catalog.New(store, service)
	if err != nil {
		t.Fatal(err)
	}
	p := principal("controller")
	db, err := resources.Databases.Create(ctx, p, catalog.DatabaseCreate{Name: "cursor-deleted", Provider: "deployment"})
	if err != nil {
		t.Fatal(err)
	}
	if err := resources.Databases.Delete(ctx, p, db.ID); err != nil {
		t.Fatal(err)
	}
	gateway, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("cursor-gateway"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"database", "gateway"} {
		t.Run(name, func(t *testing.T) {
			queries.counts.Store(0)
			queries.reads.Store(0)
			if name == "database" {
				rows, more, err := resources.Databases.Deleted(ctx, p, "", 100)
				if err != nil || len(rows) != 1 || rows[0].ID != db.ID || more {
					t.Fatal("database recovery page", err)
				}
			} else {
				ids, err := service.ReconcileIDs(ctx, p, "")
				if err != nil || len(ids) != 1 || ids[0] != gateway.ID {
					t.Fatal("Gateway recovery page", err)
				}
			}
			if queries.counts.Load() != 0 || queries.reads.Load() != 1 {
				t.Fatalf("recovery page ran %d counts and %d reads; want no count and one read", queries.counts.Load(), queries.reads.Load())
			}
			queries.counts.Store(0)
			queries.reads.Store(0)
			if name == "database" {
				_, _, err = resources.Databases.Deleted(ctx, principal("outsider"), "", 100)
			} else {
				_, err = service.ReconcileIDs(ctx, principal("outsider"), "")
			}
			if !errors.Is(err, gateways.ErrForbidden) || queries.reads.Load() != 0 {
				t.Fatal("unauthorized recovery read", err)
			}
		})
	}
}
