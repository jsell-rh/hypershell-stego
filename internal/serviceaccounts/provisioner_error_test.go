package serviceaccounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestCleanupRPCSelectsRetryableErrors(t *testing.T) {
	for code := codes.OK; code <= codes.Unauthenticated; code++ {
		t.Run(code.String(), func(t *testing.T) {
			input := status.Error(code, "private provider response")
			if input != nil {
				input = fmt.Errorf("private wrapper: %w", input)
			}
			result := terminalError(input)
			if code == codes.OK || code == codes.NotFound {
				if result != nil {
					t.Fatal("terminal cleanup changed", result)
				}
				return
			}
			retry := code == codes.Aborted || code == codes.Unavailable || code == codes.ResourceExhausted || code == codes.DeadlineExceeded
			if !errors.Is(result, ErrUnavailable) || errors.Is(result, errRetryableCleanup) != retry || result.Error() != ErrUnavailable.Error() || strings.Contains(result.Error(), "private") {
				t.Fatal("invalid cleanup error policy", result)
			}
		})
	}
	for _, row := range []struct {
		err   error
		retry bool
	}{
		{context.Canceled, false}, {context.DeadlineExceeded, true},
		{errors.Join(context.Canceled, context.DeadlineExceeded), false}, {errors.New("private unknown error"), false},
	} {
		result := terminalError(row.err)
		if !errors.Is(result, ErrUnavailable) || errors.Is(result, errRetryableCleanup) != row.retry || strings.Contains(result.Error(), "private") {
			t.Fatal(result)
		}
	}
}
