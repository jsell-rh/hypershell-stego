package acceptance

import (
	"context"
	"errors"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

func TestFailedRoleReductionCommitsRevocationBeforeCleanup(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	ctx := context.Background()
	input := accountInput("failed-reduction")
	input.Role = serviceaccounts.RoleAdmin
	created, err := service.Create(ctx, principal("alice"), gateway.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec("UPDATE role_bindings SET role_id=(SELECT id FROM roles WHERE name='gateway:viewer') WHERE gateway_id=$1", gateway.ID); err != nil {
		t.Fatal(err)
	}
	provider.failChange = true
	if err := service.Recover(ctx, gateway.ID, created.Account.ID); !errors.Is(err, serviceaccounts.ErrUnavailable) {
		t.Fatalf("failed cleanup: %v", err)
	}
	stored, err := f.storage.Get(ctx, "ServiceAccount", created.Account.ID)
	if err != nil || stored.(model.ServiceAccount).Status != "revoking" {
		t.Fatal("failed reduction did not commit terminal intent")
	}
	var audits int
	if err := f.db.QueryRow("SELECT count(*) FROM service_account_audits WHERE service_account_id=$1 AND action='revoke' AND outcome='started'", created.Account.ID).Scan(&audits); err != nil || audits != 1 {
		t.Fatal("terminal intent has no atomic audit")
	}
	// Restored access cannot cancel revocation after process state is discarded.
	if _, err := f.db.Exec("UPDATE role_bindings SET role_id=(SELECT id FROM roles WHERE name='gateway:owner') WHERE gateway_id=$1", gateway.ID); err != nil {
		t.Fatal(err)
	}
	provider.failChange = false
	restarted, err := serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.Recover(ctx, gateway.ID, created.Account.ID); err != nil {
		t.Fatal(err)
	}
	stored, err = f.storage.Get(ctx, "ServiceAccount", created.Account.ID)
	if err != nil || stored.(model.ServiceAccount).Status != "revoked" {
		t.Fatal("restart did not complete revocation")
	}
	if _, present := provider.clients[created.Account.ID]; present {
		t.Fatal("restored owner access retained the old credential")
	}
}

func TestRoleReductionCannotRiseAfterFailedCompletion(t *testing.T) {
	for _, pending := range []string{"current state", "earlier state"} {
		t.Run(pending, func(t *testing.T) {
			f := database(t)
			provider := newAccountProvider()
			service, gateway := accountService(t, f, provider)
			ctx := context.Background()
			input := accountInput("interrupted-reduction")
			input.Role = serviceaccounts.RoleAdmin
			created, err := service.Create(ctx, principal("alice"), gateway.ID, input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec("UPDATE role_bindings SET role_id=(SELECT id FROM roles WHERE name='gateway:viewer') WHERE gateway_id=$1", gateway.ID); err != nil {
				t.Fatal(err)
			}
			// The provider accepts the new role, but the completion transaction fails.
			if _, err := f.db.Exec("ALTER TABLE service_account_audits ADD CONSTRAINT reject_reconcile_completion CHECK(action <> 'reconcile' OR outcome <> 'succeeded') NOT VALID"); err != nil {
				t.Fatal(err)
			}
			if err := service.Recover(ctx, gateway.ID, created.Account.ID); err == nil {
				t.Fatal("completion fault did not fail")
			}
			if provider.roles[created.Account.ID] != serviceaccounts.RoleUser {
				t.Fatal("provider did not accept the lower role")
			}
			if pending == "earlier state" {
				if _, err := f.db.Exec("UPDATE service_accounts SET role='openshell-admin' WHERE id=$1 AND status='degraded'", created.Account.ID); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := f.db.Exec("ALTER TABLE service_account_audits DROP CONSTRAINT reject_reconcile_completion"); err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec("UPDATE role_bindings SET role_id=(SELECT id FROM roles WHERE name='gateway:owner') WHERE gateway_id=$1", gateway.ID); err != nil {
				t.Fatal(err)
			}
			restarted, err := serviceaccounts.New(f.storage, provider)
			if err != nil {
				t.Fatal(err)
			}
			if err := restarted.Recover(ctx, gateway.ID, created.Account.ID); err != nil {
				t.Fatal(err)
			}
			stored, err := f.storage.Get(ctx, "ServiceAccount", created.Account.ID)
			if err != nil {
				t.Fatal(err)
			}
			row := stored.(model.ServiceAccount)
			if row.Status != "ready" || row.Role != serviceaccounts.RoleUser || provider.roles[row.ID] != serviceaccounts.RoleUser {
				t.Fatal("restored access raised the role after failed completion")
			}
		})
	}
}
