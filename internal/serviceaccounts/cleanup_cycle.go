package serviceaccounts

import (
	"context"
	"errors"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
)

// Account and inventory cleanup share the same application time policy. STEGO
// owns admission, key ordering, saved progress, and worker cancellation.
func scanGatewayCleanup[T any](ctx context.Context, sourceVersion string, access runtime.CheckpointAccess, source runtime.CursorSource[T], emit func(context.Context, T) error, pageSize, workers int, key func(T) string) (runtime.CycleState, error) {
	scan := runtime.ScanOptions{PageSize: pageSize, MaxPages: 1, PageTimeout: time.Second}
	budget := runtime.ObservationOptions{WorkTimeout: 2 * time.Second, CommitTimeout: time.Second}
	continueOnError := func(err error) bool { return !errors.Is(err, runtime.ErrScanContract) }
	if workers == 1 {
		return runtime.ScanCycleWithOptions(ctx, sourceVersion, access, source, emit, continueOnError, scan, budget, runtime.CycleOptions{ActionTimeout: 750 * time.Millisecond})
	}
	return runtime.ScanCycleParallel(ctx, sourceVersion, access, source, emit, continueOnError, scan, budget, runtime.ParallelCycleOptions[T]{Workers: workers, ActionTimeout: 750 * time.Millisecond, Key: key})
}
