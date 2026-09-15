package acceptance

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
)

const projectionCallers = 8

// Hold the first user reads until every caller has a transaction snapshot.
// Each transaction still uses the real generated store and PostgreSQL server.
type projectionReadBarrier struct {
	gateways.Repository
	reads atomic.Int32
	ready chan struct{}
}

func (b *projectionReadBarrier) WithTransaction(ctx context.Context, fn func(context.Context, storage.Transaction) error) error {
	return b.Repository.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		reader, ok := tx.(storage.CursorReader)
		if !ok {
			return errors.New("projection fixture requires cursor reads")
		}
		return fn(ctx, &projectionBarrierTransaction{Transaction: tx, CursorReader: reader, barrier: b})
	})
}

type projectionBarrierTransaction struct {
	storage.Transaction
	storage.CursorReader
	barrier *projectionReadBarrier
}

func (tx *projectionBarrierTransaction) List(ctx context.Context, entity, field, value string, options storage.ListOptions) (storage.ListResult, error) {
	result, err := tx.Transaction.List(ctx, entity, field, value, options)
	if err != nil || entity != "User" {
		return result, err
	}
	arrival := tx.barrier.reads.Add(1)
	if arrival == projectionCallers {
		close(tx.barrier.ready)
	}
	if arrival <= projectionCallers {
		select {
		case <-ctx.Done():
			return storage.ListResult{}, ctx.Err()
		case <-tx.barrier.ready:
		}
	}
	return result, nil
}

func TestConcurrentGlobalRoleProjection(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	wave := func(name string, p gateways.Principal) {
		t.Helper()
		barrier := &projectionReadBarrier{Repository: f.storage, ready: make(chan struct{})}
		service, err := gateways.New(barrier)
		if err != nil {
			t.Fatal(err)
		}
		type result struct {
			retries int
			err     error
		}
		start := make(chan struct{})
		results := make(chan result, projectionCallers)
		var pending sync.WaitGroup
		for caller := range projectionCallers {
			pending.Add(1)
			go func() {
				defer pending.Done()
				<-start
				_, retries, err := requestWithConflictRetry(ctx, caller, func(ctx context.Context) (string, error) {
					return "", service.PrepareRequest(ctx, p)
				})
				results <- result{retries, err}
			}()
		}
		close(start)
		pending.Wait()
		close(results)
		retries := 0
		for result := range results {
			retries += result.retries
			if result.err != nil {
				t.Fatalf("concurrent %s failed after %d retries: %v", name, result.retries, result.err)
			}
		}
		if barrier.reads.Load() < projectionCallers || retries == 0 {
			t.Fatal("concurrent projection did not exercise conflicts", name)
		}
		t.Logf("Concurrent %s completed for %d callers with %d conflict retries", name, projectionCallers, retries)
	}
	wave("creation", principal("concurrent", "gateway:creator", "platform:admin"))
	if count(t, f.db, "users") != 1 || count(t, f.db, "role_bindings") != 2 || count(t, f.db, "stego_outbox.messages") != 2 {
		t.Fatal("concurrent requests duplicated creation")
	}
	wave("removal", principal("concurrent"))
	var live int
	if err := f.db.QueryRow("SELECT count(*) FROM role_bindings WHERE deleted_at IS NULL").Scan(&live); err != nil || live != 0 {
		t.Fatal("concurrent removal retained roles", err)
	}
	if count(t, f.db, "role_bindings") != 2 || count(t, f.db, "stego_outbox.messages") != 4 {
		t.Fatal("concurrent requests duplicated deletion")
	}
}
