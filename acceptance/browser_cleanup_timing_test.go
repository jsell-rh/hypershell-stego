package acceptance

import (
	"encoding/json"
	"testing"
	"time"
)

// These times describe sequential reads, not the time of a provider action.
// A later pending read invalidates the earlier completion observation.
type cleanupStageObservation struct {
	LastPendingSeconds   *float64 `json:"last_pending_observed_seconds,omitempty"`
	FirstCompleteSeconds *float64 `json:"first_complete_after_last_pending_seconds,omitempty"`
	Regressions          int      `json:"observed_regressions"`
}

func (sample *gatewayCleanupTimingSample) observeCleanup(stage string, complete bool, elapsed time.Duration) {
	if sample.Observations == nil {
		sample.Observations = make(map[string]cleanupStageObservation)
	}
	observation := sample.Observations[stage]
	seconds := elapsed.Seconds()
	if complete {
		if observation.FirstCompleteSeconds == nil {
			observation.FirstCompleteSeconds = &seconds
		}
	} else {
		if observation.FirstCompleteSeconds != nil {
			observation.Regressions++
		}
		observation.FirstCompleteSeconds = nil
		observation.LastPendingSeconds = &seconds
	}
	sample.Observations[stage] = observation
}

func TestCleanupTimingObservationWindow(t *testing.T) {
	t.Run("first-completion-and-zero-pending", func(t *testing.T) {
		var sample gatewayCleanupTimingSample
		sample.observeCleanup("accounts", false, 0)
		sample.observeCleanup("accounts", true, 5*time.Second)
		sample.observeCleanup("accounts", true, 9*time.Second)
		observed := sample.Observations["accounts"]
		if observed.LastPendingSeconds == nil || *observed.LastPendingSeconds != 0 || observed.FirstCompleteSeconds == nil || *observed.FirstCompleteSeconds != 5 || observed.Regressions != 0 {
			t.Fatal("Repeated confirmation changed the completion window", observed)
		}
		data, err := json.Marshal(observed)
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if json.Unmarshal(data, &fields) != nil || fields["last_pending_observed_seconds"] != float64(0) {
			t.Fatal("The pending observation at zero was lost")
		}
	})
	t.Run("reopened-stage", func(t *testing.T) {
		var sample gatewayCleanupTimingSample
		sample.observeCleanup("accounts", true, time.Second)
		sample.observeCleanup("sql", true, 2*time.Second)
		sample.observeCleanup("accounts", false, 3*time.Second)
		if sample.Observations["accounts"].FirstCompleteSeconds != nil {
			t.Fatal("A reopened stage still claims completion")
		}
		sample.observeCleanup("accounts", false, 4*time.Second)
		sample.observeCleanup("accounts", true, 5*time.Second)
		observed := sample.Observations["accounts"]
		if observed.LastPendingSeconds == nil || *observed.LastPendingSeconds != 4 || observed.FirstCompleteSeconds == nil || *observed.FirstCompleteSeconds != 5 || observed.Regressions != 1 {
			t.Fatal("The latest completion window is incorrect", observed)
		}
		if other := sample.Observations["sql"]; other.FirstCompleteSeconds == nil || *other.FirstCompleteSeconds != 2 || other.LastPendingSeconds != nil || other.Regressions != 0 {
			t.Fatal("One stage changed another stage's evidence")
		}
	})
	t.Run("no-invented-pending-read", func(t *testing.T) {
		var sample gatewayCleanupTimingSample
		sample.observeCleanup("proof", true, time.Second)
		data, err := json.Marshal(sample.Observations["proof"])
		if err != nil {
			t.Fatal(err)
		}
		var fields map[string]any
		if err := json.Unmarshal(data, &fields); err != nil {
			t.Fatal(err)
		}
		if _, exists := fields["last_pending_observed_seconds"]; exists {
			t.Fatal("A one-time proof invented a pending observation")
		}
	})
}
