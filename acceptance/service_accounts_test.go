package acceptance

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

type accountProvider struct {
	roles                  map[string]string
	mu                     sync.Mutex
	clients                map[string]serviceaccounts.Credential
	disabled               map[string]bool
	calls                  int
	failCreate, failChange bool
	entered, release       chan struct{}
}

func newAccountProvider() *accountProvider {
	return &accountProvider{clients: map[string]serviceaccounts.Credential{}, roles: map[string]string{}, disabled: map[string]bool{}}
}
func (p *accountProvider) Provision(ctx context.Context, spec serviceaccounts.Spec) (serviceaccounts.Credential, error) {
	p.mu.Lock()
	p.calls++
	credential := serviceaccounts.Credential{ClientID: spec.ClientID, ClientUUID: uuid.NewString(), Subject: uuid.NewString(), Secret: "one-time-private-" + uuid.NewString()}
	p.clients[spec.ServiceAccountID] = credential
	p.roles[spec.ServiceAccountID] = spec.Role
	fail := p.failCreate
	entered, release := p.entered, p.release
	p.mu.Unlock()
	if spec.AccessTokenLifetimeSeconds != 300 || spec.ExpectedIssuer != "https://issuer.example/realms/gateway" || spec.GatewayClientID != "gateway-audience" {
		return serviceaccounts.Credential{}, errors.New("incorrect provider contract")
	}
	if entered != nil {
		close(entered)
		select {
		case <-release:
		case <-ctx.Done():
			return serviceaccounts.Credential{}, ctx.Err()
		}
	}
	if fail {
		return serviceaccounts.Credential{}, errors.New("private provider error with secret " + credential.Secret)
	}
	return credential, nil
}
func (p *accountProvider) Disable(_ context.Context, gatewayID, id, clientUUID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failChange {
		return errors.New("private disable failure")
	}
	p.disabled[id] = true
	return nil
}
func (p *accountProvider) Revoke(_ context.Context, gatewayID, id, clientUUID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failChange {
		return errors.New("private revoke failure")
	}
	p.disabled[id] = true
	delete(p.clients, id)
	return nil
}
func (p *accountProvider) Delete(_ context.Context, gatewayID, id, clientUUID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failChange {
		return errors.New("private delete failure")
	}
	delete(p.clients, id)
	return nil
}
func accountService(t testing.TB, f *fixture, p *accountProvider) (*serviceaccounts.Service, model.Gateway) {
	t.Helper()
	request := f.request("accounts")
	request.OIDC = pointer(`{"issuer":"https://issuer.example/realms/gateway","client_id":"gateway-audience","audience":"gateway-audience"}`)
	row, err := f.service.Create(context.Background(), principal("alice", "gateway:creator"), request)
	if err != nil {
		t.Fatal(err)
	}
	service, err := serviceaccounts.New(f.storage, p)
	if err != nil {
		t.Fatal(err)
	}
	return service, observeGatewayFixture(t, f, row.ID)
}
func accountInput(name string) serviceaccounts.CreateRequest {
	return serviceaccounts.CreateRequest{Name: name}
}
func grantViewer(t *testing.T, f *fixture, gatewayID, name string) {
	t.Helper()
	if _, err := f.service.List(context.Background(), principal(name), 1, 20); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`INSERT INTO role_bindings(id,user_id,role_id,gateway_id,scope) SELECT $1,u.id,r.id,$2,'gateway' FROM users u,roles r WHERE u.username=$3 AND r.name='gateway:viewer'`, ksuid.New().String(), gatewayID, name); err != nil {
		t.Fatal(err)
	}
}
func TestServiceAccountLifecycleUsesGeneratedStorage(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	ctx := context.Background()
	created, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("nightly"))
	if err != nil {
		t.Fatal(err)
	}
	row := created.Account
	if !validAccountID(row.ID) || row.Status != "ready" || created.Secret == "" || row.ClientID != "hs-sa-"+gateway.ID+"-"+row.ID || created.Connection.ClientID != row.ClientID || created.Connection.TokenEndpoint != "https://issuer.example/realms/gateway/protocol/openid-connect/token" || row.Subject == "" {
		t.Fatalf("create contract: %+v %v", row, err)
	}
	// Generated models and audit records have no secret fields or secret data.
	for _, table := range []string{"service_accounts", "service_account_audits", "stego_outbox.messages"} {
		var data string
		if err := f.db.QueryRow("SELECT COALESCE(json_agg(t)::text,'[]') FROM " + table + " t").Scan(&data); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(data, created.Secret) || strings.Contains(data, "client_secret\":") || strings.Contains(data, "access_token") {
			t.Fatal("credential entered durable storage", table)
		}
	}
	got, connection, err := service.Get(ctx, principal("alice"), gateway.ID, row.ID)
	if err != nil || got.ID != row.ID || connection.ClientID != row.ClientID {
		t.Fatalf("get: %v %v", got, err)
	}
	if count(t, f.db, "service_account_audits") != 2 {
		t.Fatal("creation audit is incomplete")
	}
	if err := f.service.Delete(ctx, principal("alice"), gateway.ID); !errors.Is(err, gateways.ErrServiceAccountsExist) {
		t.Fatalf("Gateway bypassed account cleanup: %v", err)
	}
	if _, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("NIGHTLY")); !errors.Is(err, storage.ErrConflict) {
		t.Fatalf("active name uniqueness: %v", err)
	}
	grantViewer(t, f, gateway.ID, "bob")
	input := accountInput("viewer")
	input.Role = serviceaccounts.RoleAdmin
	if _, err := service.Create(ctx, principal("bob"), gateway.ID, input); !errors.Is(err, serviceaccounts.ErrRole) {
		t.Fatalf("viewer role escalation: %v", err)
	}
	input.Role = serviceaccounts.RoleUser
	viewer, err := service.Create(ctx, principal("bob"), gateway.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.Get(ctx, principal("bob"), gateway.ID, row.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("viewer read another creator: %v", err)
	}
	list, access, err := service.List(ctx, principal("bob"), gateway.ID, 1, 20)
	if err != nil || list.Total != 1 || access.Owner || list.Items.([]model.ServiceAccount)[0].ID != viewer.Account.ID {
		t.Fatalf("viewer list: %+v %+v %v", list, access, err)
	}
	list, access, err = service.List(ctx, principal("alice"), gateway.ID, 1, 1)
	if err != nil || list.Total != 2 || !access.Owner || len(list.Items.([]model.ServiceAccount)) != 1 {
		t.Fatalf("owner list: %+v %+v %v", list, access, err)
	}
	if _, _, err := service.List(ctx, principal("admin", "platform:admin"), gateway.ID, 1, 20); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("admin got implicit account access: %v", err)
	}
	provider.failChange = true
	pending, complete, err := service.Change(ctx, principal("alice"), gateway.ID, row.ID, false)
	if err != nil || complete || pending.Status != "revoking" {
		t.Fatalf("pending revoke: %+v %t %v", pending, complete, err)
	}
	persisted, err := f.storage.Get(ctx, "ServiceAccount", row.ID)
	if err != nil || persisted.(model.ServiceAccount).Status != "revoking" {
		t.Fatal("revocation intent was not durable", err)
	}
	provider.failChange = false
	restarted, err := serviceaccounts.New(f.storage, provider)
	if err != nil {
		t.Fatal(err)
	}
	revoked, complete, err := restarted.Change(ctx, principal("alice"), gateway.ID, row.ID, false)
	if err != nil || !complete || revoked.Status != "revoked" || revoked.Active || revoked.ActiveName != nil || revoked.RevokedAt == nil || !provider.disabled[row.ID] {
		t.Fatalf("revoke recovery: %+v %t %v", revoked, complete, err)
	}
	replacement, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("nightly"))
	if err != nil {
		t.Fatalf("terminal name was not released: %v", err)
	}
	for _, id := range []string{row.ID, viewer.Account.ID, replacement.Account.ID} {
		if _, complete, err := service.Change(ctx, principal("alice"), gateway.ID, id, true); err != nil || !complete {
			t.Fatalf("delete: %t %v", complete, err)
		}
	}
	if len(provider.clients) != 0 {
		t.Fatal("provider clients survived deletion")
	}
	if _, _, err := service.Get(ctx, principal("alice"), gateway.ID, row.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("deleted account read: %v", err)
	}
	if err := f.service.Delete(ctx, principal("alice"), gateway.ID); err != nil {
		t.Fatal(err)
	}
	if count(t, f.db, "service_account_audits") < 8 {
		t.Fatal("deletion removed audit history")
	}
}
func validAccountID(id string) bool {
	parsed, err := ksuid.Parse(id)
	return err == nil && parsed != ksuid.Nil
}

func TestServiceAccountCreationFailuresAndRevokedAccess(t *testing.T) {
	for _, scenario := range []string{"reservation-audit", "provider-reply", "final-audit", "access-revoked"} {
		t.Run(scenario, func(t *testing.T) {
			f := database(t)
			provider := newAccountProvider()
			service, gateway := accountService(t, f, provider)
			ctx := context.Background()
			switch scenario {
			case "reservation-audit":
				if _, err := f.db.Exec(`ALTER TABLE service_account_audits ADD CONSTRAINT reject_audit CHECK(false) NOT VALID`); err != nil {
					t.Fatal(err)
				}
			case "provider-reply":
				provider.failCreate = true
			case "final-audit":
				if _, err := f.db.Exec(`ALTER TABLE service_account_audits ADD CONSTRAINT reject_audit CHECK(outcome <> 'succeeded') NOT VALID`); err != nil {
					t.Fatal(err)
				}
			case "access-revoked":
				provider.entered = make(chan struct{})
				provider.release = make(chan struct{})
			}
			type result struct {
				created serviceaccounts.Created
				err     error
			}
			done := make(chan result, 1)
			go func() {
				created, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("fail"))
				done <- result{created, err}
			}()
			if scenario == "access-revoked" {
				select {
				case <-provider.entered:
				case <-time.After(3 * time.Second):
					t.Fatal("provider not called")
				}
				if _, err := f.db.Exec(`UPDATE role_bindings SET deleted_at=now() WHERE gateway_id=$1`, gateway.ID); err != nil {
					t.Fatal(err)
				}
				close(provider.release)
			}
			resultValue := <-done
			if resultValue.err == nil || resultValue.created.Secret != "" || resultValue.created.Account.ID != "" {
				t.Fatal("failed creation returned a credential")
			}
			if strings.Contains(resultValue.err.Error(), "one-time-private-") {
				t.Fatal("provider secret escaped through error")
			}
			if scenario == "reservation-audit" {
				if count(t, f.db, "service_accounts") != 0 || provider.calls != 0 {
					t.Fatal("failed reservation had effects")
				}
				return
			}
			if scenario == "final-audit" {
				if _, err := f.db.Exec(`ALTER TABLE service_account_audits DROP CONSTRAINT reject_audit`); err != nil {
					t.Fatal(err)
				}
			}
			rows, err := f.storage.List(ctx, "ServiceAccount", "gateway_id", gateway.ID, storage.ListOptions{Page: 1, Size: 100})
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range rows.Items.([]model.ServiceAccount) {
				if err := service.Recover(ctx, gateway.ID, row.ID); err != nil {
					t.Fatal(err)
				}
			}
			if len(provider.clients) != 0 {
				t.Fatal("failed creation left a provider client")
			}
		})
	}
}

func TestServiceAccountQuotaAndExpirationValidation(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	ctx := context.Background()
	for i := range 10 {
		if _, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput(fmt.Sprintf("account-%d", i))); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("excess")); !errors.Is(err, serviceaccounts.ErrQuota) {
		t.Fatalf("creator quota: %v", err)
	}
	for _, expires := range []string{"bad", time.Now().Add(30 * time.Minute).Format(time.RFC3339), time.Now().Add(366 * 24 * time.Hour).Format(time.RFC3339)} {
		input := accountInput("invalid-expiry")
		input.ExpiresAt = &expires
		if _, err := service.Create(ctx, principal("alice"), gateway.ID, input); !errors.Is(err, gateways.ErrInvalid) {
			t.Fatalf("expiry validation: %v", err)
		}
	}
	// The provider result is not a model and cannot be read from a later response.
	result, _, err := service.List(ctx, principal("alice"), gateway.ID, 1, 100)
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(result.Items)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "one-time-private-") {
		t.Fatal("list exposed a secret")
	}
}

func (p *accountProvider) Reconcile(_ context.Context, spec serviceaccounts.Spec, clientUUID, subject string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failChange {
		return errors.New("reconcile failed")
	}
	credential, ok := p.clients[spec.ServiceAccountID]
	if !ok || credential.ClientUUID != clientUUID || credential.Subject != subject {
		return errors.New("provider identity mismatch")
	}
	p.roles[spec.ServiceAccountID] = spec.Role
	return nil
}

func TestServiceAccountRoleCeilingAndExpiry(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	ctx := context.Background()
	input := accountInput("admin")
	input.Role = serviceaccounts.RoleAdmin
	created, err := service.Create(ctx, principal("alice"), gateway.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	id := created.Account.ID
	if _, err := f.db.Exec(`UPDATE role_bindings SET role_id=(SELECT id FROM roles WHERE name='gateway:viewer') WHERE gateway_id=$1`, gateway.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Recover(ctx, gateway.ID, id); err != nil {
		t.Fatal(err)
	}
	stored, err := f.storage.Get(ctx, "ServiceAccount", id)
	if err != nil || stored.(model.ServiceAccount).Role != serviceaccounts.RoleUser || stored.(model.ServiceAccount).Status != "ready" || provider.roles[id] != serviceaccounts.RoleUser {
		t.Fatalf("downgrade did not converge: %v %v", stored, err)
	}
	if _, err := f.db.Exec(`UPDATE role_bindings SET deleted_at=now() WHERE gateway_id=$1`, gateway.ID); err != nil {
		t.Fatal(err)
	}
	runAccess, cancelAccess := context.WithCancel(ctx)
	accessDone := make(chan error, 1)
	go func() { accessDone <- service.Run(runAccess) }()
	accessDeadline := time.Now().Add(12 * time.Second)
	for {
		value, err := f.storage.Get(ctx, "ServiceAccount", id)
		if err != nil {
			cancelAccess()
			<-accessDone
			t.Fatal(err)
		}
		if value.(model.ServiceAccount).Status == "revoked" {
			break
		}
		if time.Now().After(accessDeadline) {
			cancelAccess()
			<-accessDone
			t.Fatal("background grant revocation did not complete")
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancelAccess()
	if err := <-accessDone; err != nil {
		t.Fatal(err)
	}
	stored, err = f.storage.Get(ctx, "ServiceAccount", id)
	if err != nil || stored.(model.ServiceAccount).Status != "revoked" || !provider.disabled[id] {
		t.Fatalf("grant removal left account active: %v %v", stored, err)
	}
	if _, err := f.db.Exec(`UPDATE role_bindings SET deleted_at=NULL,role_id=(SELECT id FROM roles WHERE name='gateway:owner') WHERE gateway_id=$1`, gateway.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.Recover(ctx, gateway.ID, id); err != nil {
		t.Fatal(err)
	}
	stored, err = f.storage.Get(ctx, "ServiceAccount", id)
	if err != nil || stored.(model.ServiceAccount).Status != "revoked" || provider.roles[id] != serviceaccounts.RoleUser {
		t.Fatal("restored grant raised a terminal account")
	}
	expired, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("expiry"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE service_accounts SET expires_at=now()-interval '1 second' WHERE id=$1`, expired.Account.ID); err != nil {
		t.Fatal(err)
	}
	run, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- service.Run(run) }()
	defer func() {
		cancel()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(12 * time.Second)
	for {
		stored, err := f.storage.Get(ctx, "ServiceAccount", expired.Account.ID)
		if err != nil {
			t.Fatal(err)
		}
		if stored.(model.ServiceAccount).Status == "expired" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background expiry did not complete")
		}
		time.Sleep(20 * time.Millisecond)
	}
	provider.mu.Lock()
	disabled := provider.disabled[expired.Account.ID]
	provider.mu.Unlock()
	if !disabled {
		t.Fatal("expired account was not disabled")
	}
}

func TestServiceAccountCreationSerializesGatewayDeletion(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	provider.entered = make(chan struct{})
	provider.release = make(chan struct{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	created := make(chan error, 1)
	go func() {
		_, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("racing"))
		created <- err
	}()
	select {
	case <-provider.entered:
	case <-ctx.Done():
		t.Fatal("provisioner was not called")
	}
	deleted := make(chan error, 1)
	go func() { deleted <- f.service.Delete(ctx, principal("alice"), gateway.ID) }()
	close(provider.release)
	if err := <-created; err != nil {
		t.Fatal(err)
	}
	if err := <-deleted; !errors.Is(err, gateways.ErrServiceAccountsExist) {
		t.Fatalf("concurrent Gateway deletion: %v", err)
	}
	if _, err := f.service.Get(ctx, principal("alice"), gateway.ID); err != nil {
		t.Fatal("Gateway disappeared during account creation", err)
	}
}

func TestServiceAccountCanceledAndAbandonedCreation(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	provider.entered = make(chan struct{})
	provider.release = make(chan struct{})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		created, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("canceled"))
		if created.Secret != "" {
			done <- errors.New("canceled creation returned a secret")
			return
		}
		done <- err
	}()
	select {
	case <-provider.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("provisioner was not called")
	}
	cancel()
	if err := <-done; err == nil {
		t.Fatal("canceled creation succeeded")
	}
	if len(provider.clients) != 0 {
		t.Fatal("canceled creation left a provider client")
	}
	provider.entered = nil
	provider.release = nil
	created, err := service.Create(context.Background(), principal("alice"), gateway.ID, accountInput("seed"))
	if err != nil {
		t.Fatal(err)
	}
	row := created.Account
	row.ID = ksuid.New().String()
	row.Name = "abandoned"
	row.ActiveName = pointer("abandoned")
	row.ClientID = "hs-sa-" + gateway.ID + "-" + row.ID
	row.ClientUuid = ""
	row.Subject = ""
	row.Status = "provisioning"
	row.CreatedTime = time.Now().Add(-16 * time.Minute)
	if err := f.storage.Create(context.Background(), "ServiceAccount", row); err != nil {
		t.Fatal(err)
	}
	provider.clients[row.ID] = serviceaccounts.Credential{ClientID: row.ClientID, ClientUUID: uuid.NewString(), Subject: uuid.NewString(), Secret: "undelivered-abandoned-secret"}
	if err := service.Recover(context.Background(), gateway.ID, row.ID); err != nil {
		t.Fatal(err)
	}
	if _, exists := provider.clients[row.ID]; exists {
		t.Fatal("abandoned provider identity survived recovery")
	}
	if _, err := f.storage.Get(context.Background(), "ServiceAccount", row.ID); !errors.Is(err, storage.ErrNotFound) {
		t.Fatalf("abandoned reservation survived recovery: %v", err)
	}
}

func TestServiceAccountRejectsUnsafeConnectionMetadata(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	ctx := context.Background()
	for _, test := range []struct{ issuer, clientID, audience, endpoint string }{
		{"http://issuer.example", "client", "client", ""},
		{"https://user:password@issuer.example", "client", "client", ""},
		{"https://issuer.example?query=1", "client", "client", ""},
		{"https://issuer.example", "client", "other", ""},
		{"https://issuer.example", "client", "client", "http://gateway.example"},
		{"https://issuer.example", "client", "client", "javascript:alert(1)"},
	} {
		config, _ := json.Marshal(map[string]string{"issuer": test.issuer, "client_id": test.clientID, "audience": test.audience})
		value := string(config)
		if _, err := f.service.Update(ctx, principal("alice"), gateway.ID, gateways.PatchRequest{OIDC: &value, RouteAddress: &test.endpoint}); err != nil {
			t.Fatal(err)
		}
		observeGatewayFixture(t, f, gateway.ID)
		if _, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("unsafe")); !errors.Is(err, serviceaccounts.ErrNotReady) {
			t.Fatalf("unsafe connection metadata accepted: %v", err)
		}
	}
	if provider.calls != 0 || count(t, f.db, "service_accounts") != 0 {
		t.Fatal("invalid connection metadata reached provisioning")
	}
	credential := serviceaccounts.Credential{Secret: "test-secret-that-must-not-be-logged"}
	if strings.Contains(fmt.Sprintf("%v %#v", credential, credential), credential.Secret) {
		t.Fatal("credential formatting exposed a secret")
	}
	if _, err := json.Marshal(credential); err == nil {
		t.Fatal("credential permitted generic serialization")
	}
}

func TestServiceAccountGatewayQuota(t *testing.T) {
	f := database(t)
	provider := newAccountProvider()
	service, gateway := accountService(t, f, provider)
	ctx := context.Background()
	for creator := range 10 {
		name := fmt.Sprintf("creator-%d", creator)
		grantViewer(t, f, gateway.ID, name)
		for number := range 10 {
			if _, err := service.Create(ctx, principal(name), gateway.ID, accountInput(fmt.Sprintf("%s-account-%d", name, number))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, err := service.Create(ctx, principal("alice"), gateway.ID, accountInput("gateway-excess")); !errors.Is(err, serviceaccounts.ErrGatewayQuota) {
		t.Fatalf("Gateway quota: %v", err)
	}
	if count(t, f.db, "service_accounts") != 100 || provider.calls != 100 {
		t.Fatal("quota failure had a provisioning effect")
	}
}

func (p *accountProvider) DeleteGateway(_ context.Context, gatewayID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.failChange {
		return errors.New("private Gateway cleanup failure")
	}
	for id, client := range p.clients {
		if strings.HasPrefix(client.ClientID, "hs-sa-"+gatewayID+"-") {
			p.disabled[id] = true
			delete(p.clients, id)
		}
	}
	return nil
}

// observeGatewayFixture supplies a fresh result from the fixture workload provider.
func observeGatewayFixture(t testing.TB, f *fixture, id string) model.Gateway {
	t.Helper()
	controller := principal("fixture-workload-controller")
	service, err := gateways.New(f.storage, gateways.Options{DatabaseProvider: "cnpg", ControlPlaneSubjects: []string{controller.Subject}, ControllerWritePolicy: controllerWritePolicy(t, controller.Issuer, writeGrant(controller.Subject, "observe.workload", f.cluster))})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	row, err := service.IdentityState(ctx, controller, id)
	if err != nil {
		t.Fatal(err)
	}
	row, err = service.UpdateControlPlane(ctx, controller, id, gateways.PatchRequest{Phase: pointer("Running"), Status: pointer("Healthy")}, nil, row.ResourceVersion)
	if err != nil {
		t.Fatal(err)
	}
	return row
}
