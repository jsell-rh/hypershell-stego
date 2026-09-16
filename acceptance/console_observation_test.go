package acceptance

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

func TestGatewayConsoleObservationCommitsWithWorkload(t *testing.T) {
	f := database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	row, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("console-observation"))
	if err != nil {
		t.Fatal(err)
	}
	p := principal("console-workload")
	policy := controllerWritePolicy(t, p.Issuer, writeGrant(p.Subject, "observe.workload", f.cluster), writeGrant(p.Subject, "observe.endpoint", f.cluster), writeGrant(p.Subject, "configure.console", f.cluster))
	service, err := gateways.New(f.storage, gateways.Options{ControlPlaneSubjects: []string{p.Subject}, ControllerWritePolicy: policy})
	if err != nil {
		t.Fatal(err)
	}
	read := func() model.Gateway {
		t.Helper()
		value, err := f.storage.Get(ctx, "Gateway", row.ID)
		if err != nil {
			t.Fatal(err)
		}
		return value.(model.Gateway)
	}
	before := read()
	events := count(t, f.db, "stego_outbox.messages")
	phase, state, route, console := "Running", "Healthy", "https://gateway.example.test", "https://console.example.test"
	patch := gateways.PatchRequest{Phase: &phase, Status: &state}
	if _, err := f.db.ExecContext(ctx, `ALTER TABLE gateways ADD CONSTRAINT reject_console_observation CHECK (console_address IS NULL OR console_address <> 'https://console.example.test')`); err != nil {
		t.Fatal(err)
	}
	// The third write fails after workload and route updates. The generated
	// transaction must restore all groups and must not publish an event.
	if _, err := service.UpdateControlPlane(ctx, p, row.ID, patch, &console, &route, before.ResourceVersion); err == nil {
		t.Fatal("console fault did not reject the observation")
	}
	if !reflect.DeepEqual(before, read()) || count(t, f.db, "stego_outbox.messages") != events {
		t.Fatal("failed console observation changed state or events")
	}
	if _, err := f.db.ExecContext(ctx, `ALTER TABLE gateways DROP CONSTRAINT reject_console_observation`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.UpdateControlPlane(ctx, p, row.ID, patch, &console, &route, before.ResourceVersion); err != nil {
		t.Fatal(err)
	}
	after := read()
	current := after.CurrentObservations()
	if after.ResourceGeneration != before.ResourceGeneration || after.ObservedGeneration("workload") != after.ResourceGeneration || after.ObservedGeneration("endpoint") != after.ResourceGeneration || after.ObservedGeneration("console") != after.ResourceGeneration || current.ConsoleAddress == nil || *current.ConsoleAddress != console || current.RouteAddress == nil || *current.RouteAddress != route || current.Phase == nil || *current.Phase != phase || count(t, f.db, "stego_outbox.messages") != events+1 {
		t.Fatal("console and workload observations did not commit together")
	}
	if _, err := service.UpdateControlPlane(ctx, p, row.ID, patch, &console, &route, before.ResourceVersion); !errors.Is(err, store.ErrConflict) {
		t.Fatal("stale observation did not fail", err)
	}
	if !reflect.DeepEqual(after, read()) || count(t, f.db, "stego_outbox.messages") != events+1 {
		t.Fatal("stale observation changed state or events")
	}
}
