package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/httpapi"
	"github.com/jsell-rh/hypershell-stego/internal/users"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
)

// Model a caller that retries the complete request after a conflict. Production
// transactions still execute their callback once and return conflicts to callers.
func registerWithConflictRetry(ctx context.Context, phase int, call func(context.Context) (string, error)) (string, int, error) {
	var last error
	for attempt := 0; attempt < 64; attempt++ {
		if err := ctx.Err(); err != nil {
			return "", attempt, err
		}
		id, err := call(ctx)
		if !errors.Is(err, store.ErrConflict) && !errors.Is(err, store.ErrSerialization) {
			return id, attempt, err
		}
		last = err
		delay := 5 * time.Millisecond << min(attempt, 5)
		// Give callers different wake times.
		delay += time.Duration((phase+attempt)%8) * time.Millisecond
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", attempt + 1, ctx.Err()
		case <-timer.C:
		}
	}
	return "", 64, last
}

func TestConcurrentCurrentUserRegistration(t *testing.T) { testConcurrentCurrentUser(t, false) }
func TestConcurrentCurrentUserRequests(t *testing.T)     { testConcurrentCurrentUser(t, true) }

func testConcurrentCurrentUser(t *testing.T, throughHTTP bool) {
	f := database(t)
	service, err := users.New(f.storage)
	if err != nil {
		t.Fatal(err)
	}
	call := func(ctx context.Context) (string, error) {
		row, err := service.Current(ctx, principal("new-recipient"))
		return row.ID, err
	}
	if throughHTTP {
		key, settings := issuer(t)
		_, config := broker(t, identity(t, "localhost"))
		stop, address := startApplication(t, buildApplication(t), f.dsn, config, settings...)
		defer stop()
		bearer := token(t, key, "new-recipient")
		transport := &http.Transport{Proxy: nil, MaxConnsPerHost: 8, MaxIdleConnsPerHost: 8}
		defer transport.CloseIdleConnections()
		client := &http.Client{Transport: transport, Timeout: 3 * time.Second}
		call = func(ctx context.Context) (string, error) {
			request, err := http.NewRequestWithContext(ctx, http.MethodGet, address+"/api/hypershell/v1/users/me", nil)
			if err != nil {
				return "", err
			}
			request.Header.Set("Authorization", "Bearer "+bearer)
			response, err := client.Do(request)
			if err != nil {
				return "", err
			}
			defer response.Body.Close()
			body, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
			if err != nil || len(body) > 64<<10 {
				return "", errors.New("invalid current-user response")
			}
			if response.StatusCode == http.StatusConflict {
				return "", store.ErrConflict
			}
			if response.StatusCode != http.StatusOK {
				return "", fmt.Errorf("current-user status %d", response.StatusCode)
			}
			var row httpapi.CurrentUser
			if err := json.Unmarshal(body, &row); err != nil {
				return "", err
			}
			if row.Subject != "new-recipient" || row.CreatedAt.IsZero() || row.UpdatedAt.IsZero() {
				return "", errors.New("current-user response lost identity fields")
			}
			return row.ID, nil
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	// Hold every first insert until all eight transactions have read the missing
	// identity. The barrier makes the conflict path a required part of the test.
	if _, err := f.db.ExecContext(ctx, `CREATE FUNCTION wait_registration_fixture() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM pg_advisory_xact_lock(73114, 1); RETURN NEW; END $$; CREATE TRIGGER wait_registration_fixture BEFORE INSERT ON users FOR EACH ROW EXECUTE FUNCTION wait_registration_fixture()`); err != nil {
		t.Fatal(err)
	}
	holder, err := f.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback()
	if _, err := holder.ExecContext(ctx, "SELECT pg_advisory_xact_lock(73114, 1)"); err != nil {
		t.Fatal(err)
	}
	const workers = 8
	type result struct {
		id      string
		retries int
		err     error
	}
	results := make(chan result, workers)
	start := make(chan struct{})
	var work sync.WaitGroup
	for worker := range workers {
		work.Add(1)
		go func() {
			defer work.Done()
			<-start
			id, retries, err := registerWithConflictRetry(ctx, worker, call)
			results <- result{id, retries, err}
		}()
	}
	defer func() { cancel(); holder.Rollback(); work.Wait() }()
	close(start)
	barrierDeadline := time.Now().Add(3 * time.Second)
	for {
		var waiting int
		err := f.db.QueryRowContext(ctx, `SELECT count(*) FROM pg_locks WHERE locktype='advisory' AND database=(SELECT oid FROM pg_database WHERE datname=current_database()) AND classid=73114 AND objid=1 AND NOT granted`).Scan(&waiting)
		if err != nil {
			t.Fatal(err)
		}
		if waiting == workers {
			break
		}
		if time.Now().After(barrierDeadline) {
			t.Fatal("first registration wave did not reach the database barrier", waiting)
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err := holder.Rollback(); err != nil {
		t.Fatal(err)
	}
	work.Wait()
	close(results)
	var id string
	retries := 0
	for result := range results {
		if result.err != nil || result.id == "" {
			t.Fatal("concurrent registration did not complete within its client budget", result.err, result.retries)
		}
		if id != "" && id != result.id {
			t.Fatal("concurrent registration created multiple identities")
		}
		id = result.id
		retries += result.retries
	}
	if retries == 0 {
		t.Fatal("registration test did not exercise a conflict retry")
	}
	var count int
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM users WHERE issuer=$1 AND subject=$2", "https://issuer.example", "new-recipient").Scan(&count); err != nil || count != 1 {
		t.Fatal("duplicate identity", count, err)
	}
	if err := f.db.QueryRowContext(ctx, "SELECT count(*) FROM role_bindings").Scan(&count); err != nil || count != 0 {
		t.Fatal("registration assigned a role", count, err)
	}
	t.Logf("Eight callers received one stored identity after %d conflict retries", retries)
}
