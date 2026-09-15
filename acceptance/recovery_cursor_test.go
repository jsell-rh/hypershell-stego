package acceptance

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

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

	owner := principal("alice", "gateway:creator")
	live, err := f.service.Create(ctx, owner, f.request("cursor-live"))
	if err != nil {
		t.Fatal(err)
	}
	deleted, err := f.service.Create(ctx, owner, f.request("cursor-deleted"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Delete(ctx, owner, deleted.ID); err != nil {
		t.Fatal(err)
	}
	queries.counts.Store(0)
	queries.reads.Store(0)
	ids, err := service.ReconcileIDs(ctx, principal("controller"), "")
	if err != nil || len(ids) != 2 || !slices.Contains(ids, live.ID) || !slices.Contains(ids, deleted.ID) {
		t.Fatal("recovery omitted live or deleted Gateway", err)
	}
	if queries.counts.Load() != 0 || queries.reads.Load() != 1 {
		t.Fatalf("recovery ran %d counts and %d reads", queries.counts.Load(), queries.reads.Load())
	}
	queries.counts.Store(0)
	queries.reads.Store(0)
	_, err = service.ReconcileIDs(ctx, principal("outsider"), "")
	if !errors.Is(err, gateways.ErrForbidden) || queries.reads.Load() != 0 {
		t.Fatal("unauthorized recovery read", err)
	}
}

// Deletion of an earlier row must not shift later IDs out of recovery.
func TestGatewayRecoveryCursorSurvivesEarlierDeletion(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	service, err := gateways.New(f.storage, gateways.Options{ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		t.Fatal(err)
	}
	owner := principal("cursor-owner", "gateway:creator")
	for i := range 205 {
		_, err := f.service.Create(ctx, owner, f.request(fmt.Sprintf("cursor-%03d", i)))
		if err != nil {
			t.Fatal(err)
		}
	}
	// The cursor and this fixed baseline use the database's ID collation.
	// Byte order in Go can differ from the installation's database order.
	rows, err := f.db.QueryContext(ctx, "SELECT id FROM gateways ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var expected []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		expected = append(expected, id)
	}
	if err := rows.Err(); err != nil || len(expected) != 205 {
		t.Fatal("invalid recovery baseline", len(expected), err)
	}
	rows.Close()
	var actual []string
	after := ""
	for page := range 3 {
		ids, err := service.ReconcileIDs(ctx, principal("controller"), after)
		want := 100
		if page == 2 {
			want = 5
		}
		if err != nil || len(ids) != want {
			t.Fatal("invalid recovery page", page, len(ids), err)
		}
		actual = append(actual, ids...)
		after = ids[len(ids)-1]
		if page == 0 {
			if err := f.service.Delete(ctx, owner, ids[0]); err != nil {
				t.Fatal(err)
			}
		}
	}
	if !slices.Equal(actual, expected) {
		t.Fatal("deletion changed recovery order or omitted an ID")
	}
	if ids, err := service.ReconcileIDs(ctx, principal("controller"), after); err != nil || len(ids) != 0 {
		t.Fatal("recovery did not end", err)
	}
	row, err := service.IdentityState(ctx, principal("controller"), expected[0])
	if err != nil || !row.DeletedAt.Valid {
		t.Fatal("fixture did not retain the earlier deletion", err)
	}
}
