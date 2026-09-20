package acceptance

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// A cleanup failure remains a failure. The separate read records only counts
// and fixed status fields. It does not repeat removal or print SQL or names.
func logDatabaseCleanupFailure(t interface {
	Helper()
	Logf(string, ...any)
}, admin *sql.DB, name string, operation context.Context) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	var present bool
	var sessions, lockWaits, checkpoints, checkpointWaits int
	err := admin.QueryRowContext(ctx, `SELECT
 EXISTS (SELECT 1 FROM pg_catalog.pg_database WHERE datname=$1),
 count(*) FILTER (WHERE datname=$1),
 count(*) FILTER (WHERE datname=$1 AND wait_event_type='Lock'),
 count(*) FILTER (WHERE backend_type='checkpointer'),
 count(*) FILTER (WHERE backend_type='checkpointer' AND wait_event IS NOT NULL)
 FROM pg_catalog.pg_stat_activity`, name).Scan(&present, &sessions, &lockWaits, &checkpoints, &checkpointWaits)
	if err != nil {
		t.Logf("test database cleanup diagnostics unavailable; deadline=%t canceled=%t",
			errors.Is(operation.Err(), context.DeadlineExceeded), errors.Is(operation.Err(), context.Canceled))
		return
	}
	t.Logf("test database cleanup: present=%t sessions=%d lock_waits=%d checkpointers=%d checkpoint_waits=%d deadline=%t canceled=%t",
		present, sessions, lockWaits, checkpoints, checkpointWaits,
		errors.Is(operation.Err(), context.DeadlineExceeded), errors.Is(operation.Err(), context.Canceled))
}

type databaseCleanupLog struct{ text string }

func (*databaseCleanupLog) Helper() {}
func (l *databaseCleanupLog) Logf(format string, args ...any) {
	l.text += fmt.Sprintf(format, args...)
}

func TestDatabaseFixtureCleanupDiagnostics(t *testing.T) {
	f := database(t)
	var name string
	if err := f.db.QueryRow("SELECT current_database()").Scan(&name); err != nil {
		t.Fatal(err)
	}
	operation, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
	defer cancel()
	var output databaseCleanupLog
	logDatabaseCleanupFailure(&output, f.db, name, operation)
	if !strings.Contains(output.text, "present=true") || !strings.Contains(output.text, "deadline=true canceled=false") || strings.Contains(output.text, name) || strings.Contains(output.text, f.dsn) {
		t.Fatal("cleanup diagnostics lost state or exposed a private value")
	}
	// A failed diagnostic read must not print the connection error or retry
	// database removal. The original cleanup deadline still appears.
	closed, err := sql.Open("pgx", f.dsn)
	if err != nil {
		t.Fatal(err)
	}
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	output = databaseCleanupLog{}
	logDatabaseCleanupFailure(&output, closed, name, operation)
	if output.text != "test database cleanup diagnostics unavailable; deadline=true canceled=false" {
		t.Fatal("failed diagnostic read changed the fixed failure record")
	}
}
