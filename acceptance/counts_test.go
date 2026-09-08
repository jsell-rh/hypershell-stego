package acceptance

import (
	"context"
	"errors"
	"math"
	"strings"
	"sync"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

func controllerService(t *testing.T, f *fixture) (*gateways.Service, gateways.Principal) {
	t.Helper()
	service, err := gateways.New(f.storage, gateways.Options{ControlPlaneSubjects: []string{"controller"}})
	if err != nil {
		t.Fatal(err)
	}
	return service, principal("controller")
}

func TestSandboxCountTransitionsAndEvents(t *testing.T) {
	f := database(t)
	service, controller := controllerService(t, f)
	ctx := context.Background()
	row, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("count"))
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range []struct {
		set         bool
		input, want int32
		events      int
	}{
		{false, -1, 0, 2}, // NULL becomes zero and emits an update.
		{false, -1, 0, 2}, {false, 0, 0, 2}, {true, 0, 0, 2},
		{false, 2, 2, 3}, {false, 1, 3, 4}, {false, -1, 2, 5}, {false, -20, 0, 6},
		{true, 5, 5, 7}, {true, 2, 2, 8}, {true, 2, 2, 8}, {true, -3, 0, 9},
		{true, math.MaxInt32, math.MaxInt32, 10},
	} {
		var value int32
		if step.set {
			value, err = service.SetActiveSandboxCount(ctx, controller, row.Namespace, step.input)
		} else {
			value, err = service.AdjustActiveSandboxCount(ctx, controller, row.Namespace, step.input)
		}
		if err != nil || value != step.want || count(t, f.db, "stego_outbox.messages") != step.events {
			t.Fatalf("step %+v: value=%d error=%v", step, value, err)
		}
		stored, err := f.storage.Get(ctx, "Gateway", row.ID)
		if err != nil {
			t.Fatal(err)
		}
		current := stored.(model.Gateway)
		if current.ActiveSandboxCount == nil || *current.ActiveSandboxCount != step.want || current.Name != row.Name || current.Namespace != row.Namespace || current.DatabaseID != row.DatabaseID {
			t.Fatal("count write changed another field")
		}
	}
	stored, err := f.storage.Get(ctx, "Gateway", row.ID)
	if err != nil {
		t.Fatal(err)
	}
	before := stored.(model.Gateway)
	if _, err := service.AdjustActiveSandboxCount(ctx, controller, row.Namespace, 1); !errors.Is(err, gateways.ErrCountRange) {
		t.Fatalf("count overflow accepted: %v", err)
	}
	if _, err := service.SetActiveSandboxCount(ctx, controller, row.Namespace, math.MaxInt32); err != nil {
		t.Fatal(err)
	}
	stored, err = f.storage.Get(ctx, "Gateway", row.ID)
	if err != nil {
		t.Fatal(err)
	}
	after := stored.(model.Gateway)
	if *after.ActiveSandboxCount != math.MaxInt32 || !after.UpdatedTime.Equal(before.UpdatedTime) || count(t, f.db, "stego_outbox.messages") != 10 {
		t.Fatal("overflow or equal value wrote state")
	}
	if value, err := service.AdjustActiveSandboxCount(ctx, controller, row.Namespace, math.MinInt32); err != nil || value != 0 {
		t.Fatalf("large decrement: %d %v", value, err)
	}
}

func TestSandboxCountEventFailureRollsBack(t *testing.T) {
	f := database(t)
	service, controller := controllerService(t, f)
	ctx := context.Background()
	row, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("rollback"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_count_event CHECK(kind<>'gateway.updated')`); err != nil {
		t.Fatal(err)
	}
	for _, set := range []bool{false, true} {
		var value int32
		if set {
			value, err = service.SetActiveSandboxCount(ctx, controller, row.Namespace, 8)
		} else {
			value, err = service.AdjustActiveSandboxCount(ctx, controller, row.Namespace, 1)
		}
		if err == nil || value != 0 {
			t.Fatal("event failure reported a successful count")
		}
		stored, err := f.storage.Get(ctx, "Gateway", row.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.(model.Gateway).ActiveSandboxCount != nil || count(t, f.db, "stego_outbox.messages") != 1 {
			t.Fatal("failed event committed a count")
		}
	}
}

func TestSandboxCountAccessAndMissingResources(t *testing.T) {
	f := database(t)
	service, controller := controllerService(t, f)
	ctx := context.Background()
	row, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("access"))
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range []gateways.Principal{principal("alice"), principal("admin", "platform:admin"), principal("creator", "gateway:creator"), principal("viewer", "gateway:viewer"), {Subject: "ordinary", Username: "controller"}} {
		for _, namespace := range []string{row.Namespace, "missing"} {
			if _, err := service.AdjustActiveSandboxCount(ctx, p, namespace, 1); !errors.Is(err, gateways.ErrForbidden) {
				t.Fatalf("untrusted count adjustment: %v", err)
			}
			if _, err := service.SetActiveSandboxCount(ctx, p, namespace, 1); !errors.Is(err, gateways.ErrForbidden) {
				t.Fatalf("untrusted count set: %v", err)
			}
		}
	}
	if _, err := f.service.AdjustActiveSandboxCount(ctx, controller, row.Namespace, 1); !errors.Is(err, gateways.ErrForbidden) {
		t.Fatalf("missing allowlist allowed count: %v", err)
	}
	if _, err := service.SetActiveSandboxCount(ctx, gateways.Principal{}, row.Namespace, 1); !errors.Is(err, gateways.ErrIdentity) {
		t.Fatalf("missing principal accepted: %v", err)
	}
	for _, namespace := range []string{"", " ", "bad\x00", "bad\xff", strings.Repeat("x", 254)} {
		if _, err := service.AdjustActiveSandboxCount(ctx, controller, namespace, 1); !errors.Is(err, gateways.ErrInvalid) {
			t.Fatalf("invalid namespace accepted: %v", err)
		}
	}
	if err := f.service.Delete(ctx, principal("alice"), row.ID); err != nil {
		t.Fatal(err)
	}
	before := count(t, f.db, "stego_outbox.messages")
	for _, namespace := range []string{row.Namespace, "missing", "' OR true --"} {
		if value, err := service.AdjustActiveSandboxCount(ctx, controller, namespace, 1); err != nil || value != 0 {
			t.Fatalf("absent namespace: %d %v", value, err)
		}
		if value, err := service.SetActiveSandboxCount(ctx, controller, namespace, 1); err != nil || value != 0 {
			t.Fatalf("absent namespace: %d %v", value, err)
		}
	}
	if count(t, f.db, "stego_outbox.messages") != before {
		t.Fatal("missing Gateway emitted an event")
	}
}

func TestConcurrentSandboxCountsKeepEveryIncrement(t *testing.T) {
	f := database(t)
	service, controller := controllerService(t, f)
	ctx := context.Background()
	row, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("concurrent"))
	if err != nil {
		t.Fatal(err)
	}
	const workers = 32
	start := make(chan struct{})
	results := make(chan int32, workers)
	failures := make(chan error, workers)
	var wait sync.WaitGroup
	for range workers {
		wait.Go(func() {
			<-start
			value, err := service.AdjustActiveSandboxCount(ctx, controller, row.Namespace, 1)
			results <- value
			failures <- err
		})
	}
	close(start)
	wait.Wait()
	close(results)
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
	seen := map[int32]bool{}
	for value := range results {
		if value < 1 || value > workers || seen[value] {
			t.Fatalf("repeated or invalid result: %d", value)
		}
		seen[value] = true
	}
	stored, err := f.storage.Get(ctx, "Gateway", row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if *stored.(model.Gateway).ActiveSandboxCount != workers || count(t, f.db, "stego_outbox.messages") != workers+1 {
		t.Fatal("concurrent count or event was lost")
	}
}
