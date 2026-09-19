package serviceaccountprovisioner

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/serviceaccountkeycloak"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

func TestProviderErrorRetainsSafeRetryStatus(t *testing.T) {
	for code := codes.Canceled; code <= codes.Unauthenticated; code++ {
		t.Run(code.String(), func(t *testing.T) {
			original, err := status.New(code, "private state response").WithDetails(&emptypb.Empty{})
			if err != nil {
				t.Fatal(err)
			}
			result := providerError(fmt.Errorf("private wrapper: %w", original.Err()))
			want := codes.Internal
			switch code {
			case codes.Aborted, codes.Unavailable, codes.ResourceExhausted, codes.DeadlineExceeded, codes.Canceled:
				want = code
			}
			if status.Code(result) != want || strings.Contains(result.Error(), "private") || len(status.Convert(result).Details()) != 0 {
				t.Fatal("unsafe provider status", code, result)
			}
		})
	}
	for _, row := range []struct {
		err  error
		code codes.Code
	}{
		{nil, codes.OK}, {context.Canceled, codes.Canceled}, {context.DeadlineExceeded, codes.DeadlineExceeded},
		{serviceaccountkeycloak.ErrNotFound, codes.NotFound}, {serviceaccountkeycloak.ErrNotManaged, codes.PermissionDenied},
		{errors.New("private provider response"), codes.Internal},
	} {
		result := providerError(row.err)
		if status.Code(result) != row.code || (result != nil && strings.Contains(result.Error(), "private")) {
			t.Fatal("provider policy changed", result)
		}
	}
}
