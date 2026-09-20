package gatewayworkload

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	control "github.com/jsell-rh/hypershell-stego/out/grpcapi/pb/hypershell/controlplane/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestWorkloadPendingObservationPreservesFailures(t *testing.T) {
	providerFailure := errors.New("provider failed")
	commitFailure := status.Error(codes.Aborted, "state changed")
	for _, test := range []struct {
		name         string
		work, commit error
		wantPending  bool
	}{
		{"pending", ErrPending, nil, true},
		{"ready", nil, nil, false},
		{"joined provider failure", errors.Join(ErrPending, providerFailure), nil, false},
		{"wrapped pending", fmt.Errorf("provider: %w", ErrPending), nil, false},
		{"failed pending commit", ErrPending, commitFailure, false},
		{"pending value from commit", ErrPending, ErrPending, false},
		{"provider and commit failure", providerFailure, commitFailure, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			commits := 0
			result, err := observeWorkload(context.Background(), func(context.Context) error {
				return test.work
			}, func(_ context.Context, failure error) error {
				commits++
				if !errors.Is(failure, test.work) {
					t.Fatal("commit lost the provider observation", failure)
				}
				return test.commit
			})
			if commits != 1 {
				t.Fatal("observation did not commit exactly once")
			}
			if test.wantPending {
				if err != nil || result.RecheckAfter != time.Second {
					t.Fatal("pending work did not schedule a normal check", result, err)
				}
				return
			}
			if result.RecheckAfter != 0 {
				t.Fatal("failure or completed work scheduled a pending check")
			}
			if test.work != ErrPending && !errors.Is(err, test.work) {
				t.Fatal("provider failure was lost", err)
			}
			if test.commit != nil && !errors.Is(err, test.commit) {
				t.Fatal("commit failure was lost", err)
			}
			if test.work == nil && test.commit == nil && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestWorkloadPendingCancellationPreservesFailure(t *testing.T) {
	for _, phase := range []string{"work", "commit", "work deadline"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if phase == "work deadline" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, observationCommitTimeout+25*time.Millisecond)
				defer stop()
			}
			commits := 0
			result, err := observeWorkload(ctx, func(work context.Context) error {
				if phase == "work" {
					cancel()
				} else if phase == "work deadline" {
					<-work.Done()
				}
				return ErrPending
			}, func(_ context.Context, failure error) error {
				commits++
				if phase == "work deadline" && !errors.Is(failure, context.DeadlineExceeded) {
					t.Fatal("late pending result hid the work deadline", failure)
				}
				if phase == "commit" {
					cancel()
				}
				return nil
			})
			want := error(context.Canceled)
			wantCommits := 1
			if phase == "work" {
				wantCommits = 0
			} else if phase == "work deadline" {
				want = context.DeadlineExceeded
			}
			if !errors.Is(err, want) || result.RecheckAfter != 0 || commits != wantCommits {
				t.Fatal("cancellation became pending progress", result, err, commits)
			}
		})
	}
}

func TestWorkloadPendingRecheckKeepsCleanupIncomplete(t *testing.T) {
	for _, phase := range []string{"sql", "workload", "workload conflict", "live conflict", "joined live failure"} {
		t.Run(phase, func(t *testing.T) {
			gw, release := records(t)
			history := workloadHistory(testClusterID)
			current := &stateFixture{state: &control.GetGatewayIdentityStateResponse{
				Gateway: gw, ResourceVersion: 77, ResourceGeneration: 1,
				CleanupTargets: history, Deleted: true,
			}}
			api := new(apiFixture)
			provider := &providerFixture{err: ErrPending}
			if phase == "sql" {
				history["sql"].Targets[testClusterID] = false
			} else {
				history["workload"].Targets[testClusterID] = true
			}
			switch phase {
			case "workload conflict":
				current.conflict = true
			case "live conflict":
				current.state.Deleted = false
				api.err = status.Error(codes.Aborted, "state changed")
			case "joined live failure":
				current.state.Deleted = false
				provider.err = errors.Join(ErrPending, errors.New("provider failed"))
			}
			controller, err := New(api, current, &releaseFixture{row: release}, provider)
			if err != nil {
				t.Fatal(err)
			}
			result, err := controller.reconcile(context.Background(), gw.Metadata.Id)
			switch phase {
			case "workload conflict", "live conflict":
				if status.Code(err) != codes.Aborted || result.RecheckAfter != 0 || current.state.ResourceVersion != 77 {
					t.Fatal("pending observation hid a stale state write", result, err)
				}
			case "joined live failure":
				if !errors.Is(err, provider.err) || result.RecheckAfter != 0 || api.desired != "WorkloadUnavailable" {
					t.Fatal("joined provider error became normal progress", result, err, api.desired)
				}
			default:
				if err != nil || result.RecheckAfter != time.Second || history[phase].Targets[testClusterID] {
					t.Fatal("pending cleanup was marked complete", result, err)
				}
				if phase == "sql" && (provider.sqlDeletes != 1 || provider.deletes != 0 || current.observations != 0) {
					t.Fatal("pending SQL cleanup changed workload completion")
				}
				if phase == "workload" && (provider.deletes != 1 || provider.sqlDeletes != 0 || current.observations != 1 || current.state.ResourceVersion != 78) {
					t.Fatal("pending workload did not clear the prior completion")
				}
			}
		})
	}
}
