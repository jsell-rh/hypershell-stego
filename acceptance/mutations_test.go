package acceptance

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	contract "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

func pointer[T any](value T) *T { return &value }

func TestGatewayMutationsPreserveOwnedFields(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	owner := principal("alice")
	request := f.request("original")
	request.ServerDNSNames = []string{"original.example.test"}
	request.ExternalDNS = pointer("old.example.test")
	created, err := f.service.Create(ctx, principal("alice", "gateway:creator"), request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE gateways SET active_sandbox_count=4,console_address='https://console.example.test' WHERE id=$1`, created.ID); err != nil {
		t.Fatal(err)
	}
	updated, err := f.service.Update(ctx, owner, created.ID, gateways.PatchRequest{Name: pointer("changed"), DatabaseID: pointer("ignored"), ExternalDNS: pointer(""), SupervisorImage: pointer("supervisor:v2"), CredentialDriver: pointer("driver-a"), ServerDNSNames: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Name != "changed" || updated.DatabaseID != created.DatabaseID || updated.Namespace != created.Namespace || updated.ExternalDns == nil || *updated.ExternalDns != "" || updated.SupervisorImage == nil || *updated.SupervisorImage != "supervisor:v2" || updated.CredentialDriver == nil || *updated.CredentialDriver != "driver-a" || string(updated.ServerDnsNames) != string(created.ServerDnsNames) || updated.ActiveSandboxCount == nil || *updated.ActiveSandboxCount != 4 || updated.ConsoleAddress == nil || *updated.ConsoleAddress != "https://console.example.test" {
		t.Fatalf("patch changed protected fields or lost values: %+v", updated)
	}
	if !updated.CreatedTime.Equal(created.CreatedTime) || !updated.UpdatedTime.After(created.UpdatedTime) {
		t.Fatal("patch timestamps are incorrect")
	}
	for _, patch := range []gateways.PatchRequest{
		{CredentialDriver: pointer("driver-b")}, {CredentialDriver: pointer("")},
	} {
		if _, err := f.service.Update(ctx, owner, created.ID, patch); !errors.Is(err, contract.ErrConflict) {
			t.Fatalf("driver mutation: %v", err)
		}
	}
	for _, patch := range []gateways.PatchRequest{
		{Name: pointer("")}, {Name: pointer("invalid\x00")}, {ClusterID: pointer("bad")}, {ReleaseID: pointer(ksuid.New().String())},
		{RouteAddress: pointer("invalid\x00")}, {ServerDNSNames: make([]string, 129)},
	} {
		if _, err := f.service.Update(ctx, owner, created.ID, patch); !errors.Is(err, gateways.ErrInvalid) {
			t.Fatalf("invalid patch: %v", err)
		}
	}
	if count(t, f.db, "stego_outbox.messages") != 3 {
		t.Fatal("failed update committed an event")
	}
	// Role names in a token do not substitute for a grant on this Gateway.
	for _, p := range []gateways.Principal{principal("bob", "gateway:creator"), principal("mallory", "gateway:owner"), principal("admin", "platform:admin")} {
		if _, err := f.service.Update(ctx, p, created.ID, gateways.PatchRequest{Name: pointer("denied")}); !errors.Is(err, contract.ErrNotFound) {
			t.Fatalf("unowned patch: %v", err)
		}
	}
	if _, err := f.service.List(ctx, principal("viewer"), 1, 1); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO role_bindings(id,user_id,role_id,gateway_id,scope) SELECT $1,u.id,r.id,$2,'gateway' FROM users u,roles r WHERE u.username='viewer' AND r.name='gateway:viewer'`, ksuid.New().String(), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Update(ctx, principal("viewer"), created.ID, gateways.PatchRequest{}); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("viewer patch: %v", err)
	}
	if err := f.service.Delete(ctx, principal("viewer"), created.ID); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("viewer delete: %v", err)
	}
	if _, err := f.db.Exec(`UPDATE role_bindings SET deleted_at=now() WHERE user_id=(SELECT id FROM users WHERE username='alice')`); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Delete(ctx, owner, created.ID); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("revoked owner deleted: %v", err)
	}
	if err := f.service.Delete(ctx, principal("admin", "platform:admin"), created.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.service.Get(ctx, principal("admin", "platform:admin"), created.ID); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("deleted Gateway remains visible: %v", err)
	}
	if err := f.service.Delete(ctx, principal("admin", "platform:admin"), created.ID); !errors.Is(err, contract.ErrNotFound) {
		t.Fatalf("repeat deletion: %v", err)
	}
	var deleted bool
	if err := f.db.QueryRow(`SELECT deleted_at IS NOT NULL FROM gateways WHERE id=$1`, created.ID).Scan(&deleted); err != nil || !deleted {
		t.Fatalf("missing tombstone: %v", err)
	}
	if count(t, f.db, "stego_outbox.messages") != 4 {
		t.Fatal("delete did not commit exactly one event")
	}
}

func TestMutationEventFailureRollsBack(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	owner := principal("alice")
	created, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("original"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`ALTER TABLE stego_outbox.messages ADD CONSTRAINT reject_mutations CHECK(kind IN ('gateway.created','rolebinding.created'))`); err != nil {
		t.Fatal(err)
	}
	row, err := f.service.Update(ctx, owner, created.ID, gateways.PatchRequest{Name: pointer("must roll back")})
	if err == nil || row.ID != "" {
		t.Fatal("event failure reported successful patch")
	}
	if err := f.service.Delete(ctx, owner, created.ID); err == nil {
		t.Fatal("event failure reported successful deletion")
	}
	current, err := f.service.Get(ctx, owner, created.ID)
	if err != nil || current.Name != created.Name || !current.UpdatedTime.Equal(created.UpdatedTime) {
		t.Fatalf("failed mutation changed Gateway: %+v %v", current, err)
	}
	if count(t, f.db, "stego_outbox.messages") != 2 {
		t.Fatal("failed mutation changed events")
	}
}

type pausedRepository struct {
	gateways.Repository
	read, release chan struct{}
}

func (r pausedRepository) WithTransaction(ctx context.Context, fn func(context.Context, contract.Transaction) error) error {
	return r.Repository.WithTransaction(ctx, func(ctx context.Context, tx contract.Transaction) error {
		return fn(ctx, pausedTransaction{Transaction: tx, read: r.read, release: r.release})
	})
}

type pausedTransaction struct {
	contract.Transaction
	read, release chan struct{}
}

func (tx pausedTransaction) Replace(ctx context.Context, entity, id string, value any) error {
	if entity == "Gateway" {
		close(tx.read)
		select {
		case <-tx.release:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return tx.Transaction.Replace(ctx, entity, id, value)
}

func TestConcurrentChangeCannotBeOverwrittenByGatewayPatch(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	row, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("original"))
	if err != nil {
		t.Fatal(err)
	}
	read, release := make(chan struct{}), make(chan struct{})
	service, err := gateways.New(pausedRepository{Repository: f.storage, read: read, release: release}, gateways.Options{DatabaseProvider: gateways.ProviderCNPG})
	if err != nil {
		t.Fatal(err)
	}
	result := make(chan error, 1)
	go func() {
		_, err := service.Update(ctx, principal("alice"), row.ID, gateways.PatchRequest{Name: pointer("stale")})
		result <- err
	}()
	select {
	case <-read:
	case <-time.After(3 * time.Second):
		close(release)
		t.Fatal("patch did not read current state")
	}
	_, writeErr := f.db.Exec(`UPDATE gateways SET active_sandbox_count=9 WHERE id=$1`, row.ID)
	close(release)
	select {
	case err = <-result:
	case <-time.After(12 * time.Second):
		t.Fatal("patch did not complete")
	}
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if !errors.Is(err, contract.ErrSerialization) {
		t.Fatalf("stale patch did not report a transaction conflict: %v", err)
	}
	stored, err := f.storage.Get(ctx, "Gateway", row.ID)
	if err != nil {
		t.Fatal(err)
	}
	got := stored.(model.Gateway)
	if got.Name != "original" || got.ActiveSandboxCount == nil || *got.ActiveSandboxCount != 9 || count(t, f.db, "stego_outbox.messages") != 2 {
		t.Fatal("stale patch overwrote the new state or committed an event")
	}
}

func TestControlPlaneSubjectDoesNotUseUsernameOrRoles(t *testing.T) {
	f := database(t)
	ctx := context.Background()
	row, err := f.service.Create(ctx, principal("alice", "gateway:creator"), f.request("original"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := gateways.New(f.storage, gateways.Options{DatabaseProvider: gateways.ProviderCNPG, ControlPlaneSubjects: []string{"controller-subject"}})
	if err != nil {
		t.Fatal(err)
	}
	console := pointer("https://console.example.test")
	for _, p := range []gateways.Principal{principal("alice"), principal("admin", "platform:admin"), {Issuer: "https://issuer.example", Subject: "ordinary", Username: "controller-subject", Roles: []string{"control-plane"}}} {
		if _, err := service.UpdateControlPlane(ctx, p, row.ID, gateways.PatchRequest{}, console, row.ResourceVersion); !errors.Is(err, gateways.ErrForbidden) {
			t.Fatalf("control-plane identity accepted: %v", err)
		}
	}
	controller := gateways.Principal{Issuer: "https://issuer.example", Subject: "controller-subject", Username: "controller"}
	if _, err := f.service.UpdateControlPlane(ctx, controller, row.ID, gateways.PatchRequest{}, console, row.ResourceVersion); !errors.Is(err, gateways.ErrForbidden) {
		t.Fatalf("missing allowlist accepted: %v", err)
	}
	got, err := service.UpdateControlPlane(ctx, controller, row.ID, gateways.PatchRequest{}, console, row.ResourceVersion)
	if err != nil || got.ConsoleAddress == nil || *got.ConsoleAddress != *console {
		t.Fatalf("controller update: %+v %v", got, err)
	}
}
