package cleanupmetrics

import (
	"context"
	"errors"
	"testing"
	"time"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestCleanupSummaryScopeAndTimestampChecks(t *testing.T) {
	now := timestamppb.New(time.Now())
	good := &control.CleanupSummary{Owner: "workload", Target: "cluster", Pending: 1, OldestPending: now, ObservedAt: now}
	result, err := sample(good, nil, "workload", "cluster", "")
	if err != nil || result.Pending != 1 || result.OldestPending == nil || !result.OldestPending.Equal(now.AsTime()) {
		t.Fatal(result, err)
	}
	for _, change := range []func(*control.CleanupSummary){
		func(s *control.CleanupSummary) { s.Owner = "identity" },
		func(s *control.CleanupSummary) { s.Target = "other" },
		func(s *control.CleanupSummary) { s.Provider = "deployment" },
		func(s *control.CleanupSummary) { s.ObservedAt = nil },
		func(s *control.CleanupSummary) { s.ObservedAt = &timestamppb.Timestamp{Nanos: -1} },
		func(s *control.CleanupSummary) { s.OldestPending = &timestamppb.Timestamp{Nanos: -1} },
	} {
		row := proto.Clone(good).(*control.CleanupSummary)
		change(row)
		if _, err := sample(row, nil, "workload", "cluster", ""); !errors.Is(err, runtime.ErrMetricsContract) {
			t.Fatal("invalid summary accepted", row, err)
		}
	}
	if _, err := sample(nil, nil, "workload", "cluster", ""); !errors.Is(err, runtime.ErrMetricsContract) {
		t.Fatal(err)
	}
	for _, code := range []codes.Code{codes.Unimplemented, codes.InvalidArgument} {
		if _, err := sample(nil, status.Error(code, "PRIVATE"), "", "", ""); !errors.Is(err, runtime.ErrMetricsContract) {
			t.Fatal(err)
		}
	}
	for _, code := range []codes.Code{codes.PermissionDenied, codes.Unavailable, codes.DeadlineExceeded} {
		if _, err := sample(nil, status.Error(code, "PRIVATE"), "", "", ""); status.Code(err) != code {
			t.Fatal(err)
		}
	}
	for _, source := range []func(context.Context) (runtime.CleanupSample, error){Gateway(nil, "workload", "cluster"), Database(nil, "deployment")} {
		if _, err := source(context.Background()); !errors.Is(err, runtime.ErrMetricsContract) {
			t.Fatal(err)
		}
	}
}
