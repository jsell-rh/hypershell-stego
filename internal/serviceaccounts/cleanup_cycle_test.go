package serviceaccounts

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGatewayCleanupUsesBoundedRetryPolicy(t *testing.T) {
	for _, workers := range []int{1, 8} {
		for _, row := range []struct {
			name       string
			failure    error
			persistent bool
			attempts   int
			success    bool
		}{
			{"aborted", terminalError(status.Error(codes.Aborted, "private")), false, 2, true},
			{"unavailable", terminalError(status.Error(codes.Unavailable, "private")), false, 2, true},
			{"capacity", terminalError(status.Error(codes.ResourceExhausted, "private")), false, 2, true},
			{"deadline", terminalError(context.DeadlineExceeded), false, 2, true},
			{"exhausted", terminalError(status.Error(codes.Unavailable, "private")), true, 2, false},
			{"denied", terminalError(status.Error(codes.PermissionDenied, "private")), false, 1, false},
			{"unknown", terminalError(errors.New("private")), false, 1, false},
			{"canceled", terminalError(context.Canceled), false, 1, false},
			{"bare-deadline", context.DeadlineExceeded, false, 1, false},
			{"canceled-with-marker", errors.Join(retryableCleanupFailure{}, context.Canceled), false, 1, false},
			{"contract-with-marker", errors.Join(retryableCleanupFailure{}, runtime.ErrScanContract), false, 1, false},
		} {
			t.Run(fmt.Sprintf("workers=%d/%s", workers, row.name), func(t *testing.T) {
				synctest.Test(t, func(t *testing.T) {
					saved := runtime.Checkpoint{}
					access := runtime.CheckpointAccess{
						Load: func(context.Context) (runtime.Checkpoint, error) { return saved, nil },
						Save: func(ctx context.Context, version int64, after string) error {
							if ctx.Err() != nil || version != saved.Version {
								return runtime.ErrScanContract
							}
							saved = runtime.Checkpoint{Version: version + 1, After: after}
							return nil
						},
					}
					source := func(context.Context, string, int) (runtime.CursorPage[string], error) {
						return runtime.CursorPage[string]{Items: []runtime.CursorItem[string]{{Cursor: "account", Value: "account"}}}, nil
					}
					calls := 0
					var first context.Context
					var ended time.Time
					result, err := scanGatewayCleanup(context.Background(), "input", access, source, func(ctx context.Context, account string) error {
						calls++
						end, ok := ctx.Deadline()
						if !ok || time.Until(end) != 750*time.Millisecond || account != "account" {
							t.Error("Action budget or account changed")
						}
						if calls == 1 {
							first, ended = ctx, time.Now()
							return row.failure
						}
						if first.Err() != context.Canceled || time.Since(ended) != 25*time.Millisecond {
							t.Error("Retry did not release its context or use the fixed delay")
						}
						if row.persistent {
							return row.failure
						}
						return nil
					}, 100, workers, func(value string) string { return value })
					if calls != row.attempts || (err == nil) != row.success || result.Failed == row.success {
						t.Fatal(result, err, calls)
					}
					if row.success && (!result.Complete || saved.Version != 1) {
						t.Fatal("Successful cleanup did not commit", result, saved)
					}
					if !row.success && !errors.Is(err, row.failure) {
						t.Fatal("Cleanup failure was lost", err)
					}
					if saved.Version > 0 {
						state, decodeErr := runtime.DecodeCycle(saved.After)
						if decodeErr != nil || state.Failed != result.Failed {
							t.Fatal("Saved failure evidence changed", state, decodeErr)
						}
					}
				})
			})
		}
	}
}
