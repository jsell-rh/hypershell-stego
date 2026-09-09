// Package serviceaccounts owns the Gateway automation-identity lifecycle.
package serviceaccounts

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

const RoleUser = "openshell-user"
const RoleAdmin = "openshell-admin"
const DefaultExpiration = 90 * 24 * time.Hour
const MinimumExpiration = time.Hour
const MaximumExpiration = 365 * 24 * time.Hour
const ReclaimAfter = 15 * time.Minute

var ErrUnavailable = errors.New("service-account provider is unavailable")
var ErrRole = errors.New("requested service-account role is not permitted")
var ErrQuota = errors.New("service-account quota is exhausted")
var ErrCreatorQuota = fmt.Errorf("%w: creator", ErrQuota)
var ErrGatewayQuota = fmt.Errorf("%w: gateway", ErrQuota)
var ErrNotReady = errors.New("gateway is not ready")
var ErrBusy = errors.New("service-account operation is pending")

// Spec has no secret. Stable IDs also identify an uncertain remote creation.
type Spec struct {
	ClientID, DisplayName, GatewayClientID, GatewayID, ServiceAccountID, CreatorUserID, Role, ExpectedIssuer string
	AccessTokenLifetimeSeconds                                                                               int32
}

// Credential exists only for the synchronous create result. Do not persist it.
type Credential struct{ ClientID, ClientUUID, Subject, Secret string }
type Provisioner interface {
	DeleteGateway(context.Context, string) error
	Provision(context.Context, Spec) (Credential, error)
	Reconcile(context.Context, Spec, string, string) error
	// Revoke permanently stops issuance. Later updates must not restore it.
	Revoke(context.Context, string, string, string) error
	Delete(context.Context, string, string, string) error
}
type CreateRequest struct {
	Name           string  `json:"name"`
	Description    *string `json:"description,omitempty"`
	CredentialType string  `json:"credential_type,omitempty"`
	Role           string  `json:"role,omitempty"`
	ExpiresAt      *string `json:"expires_at,omitempty"`
}
type Access struct {
	UserID string
	Owner  bool
}
type Connection struct {
	GatewayName                string `json:"gateway_name"`
	GatewayEndpoint            string `json:"gateway_endpoint,omitempty"`
	Issuer                     string `json:"issuer"`
	TokenEndpoint              string `json:"token_endpoint"`
	GrantType                  string `json:"grant_type"`
	ClientID                   string `json:"client_id"`
	Audience                   string `json:"audience"`
	AccessTokenLifetimeSeconds int32  `json:"access_token_lifetime_seconds"`
}
type Created struct {
	Account    model.ServiceAccount
	Connection Connection
	Secret     string
}
type Service struct {
	repository storage.Repository
	provider   Provisioner
	now        func() time.Time
}

func New(repository storage.Repository, provider Provisioner) (*Service, error) {
	if repository == nil {
		return nil, errors.New("service accounts require storage")
	}
	return &Service{repository: repository, provider: provider, now: time.Now}, nil
}
func validID(id string) bool {
	parsed, err := ksuid.Parse(id)
	return err == nil && parsed != ksuid.Nil && parsed.String() == id
}
func validText(value string, max int) bool {
	return len(value) <= max && utf8.ValidString(value) && !strings.ContainsRune(value, 0)
}
func allowed(access Access, role string) bool {
	return role == RoleUser || access.Owner && role == RoleAdmin
}

// Access requires an actual Gateway grant. Platform roles do not supply one.
func resolveAccess(ctx context.Context, tx storage.Storage, p gateways.Principal, gatewayID string) (Access, error) {
	user, err := gateways.ResolvePrincipal(ctx, tx, p)
	if err != nil {
		return Access{}, err
	}
	return accessForUser(ctx, tx, gatewayID, user.ID)
}
func accessForUser(ctx context.Context, tx storage.Storage, gatewayID, userID string) (Access, error) {
	value, err := tx.Get(ctx, "User", userID)
	if err != nil {
		return Access{}, err
	}
	user, ok := value.(model.User)
	if !ok {
		return Access{}, errors.New("unexpected user storage result")
	}
	if user.Issuer == nil || user.Subject == nil || *user.Issuer == "" || *user.Subject == "" {
		return Access{}, storage.ErrNotFound
	}
	access := Access{UserID: userID}
	for _, name := range []string{"gateway:owner", "gateway:viewer"} {
		roles, err := tx.List(ctx, "Role", "name", name, storage.ListOptions{Page: 1, Size: 1})
		if err != nil {
			return Access{}, err
		}
		rows, ok := roles.Items.([]model.Role)
		if !ok || len(rows) != 1 {
			return Access{}, errors.New("required Gateway role is absent")
		}
		bindings, err := tx.List(ctx, "RoleBinding", "gateway_id", gatewayID, storage.ListOptions{Page: 1, Size: 0, CountOnly: true, ImplicitFilters: map[string]string{"user_id": userID, "role_id": rows[0].ID, "scope": "gateway"}})
		if err != nil {
			return Access{}, err
		}
		if bindings.Total > 0 {
			access.Owner = name == "gateway:owner"
			return access, nil
		}
	}
	return Access{}, storage.ErrNotFound
}
func (s *Service) locked(ctx context.Context, gatewayID string, fn func(context.Context, storage.Transaction, model.Gateway) error) error {
	if !validID(gatewayID) {
		return storage.ErrNotFound
	}
	return s.repository.WithLockedResource(ctx, "Gateway", "id", gatewayID, func(ctx context.Context, tx storage.Transaction, value any) error {
		row, ok := value.(model.Gateway)
		if !ok {
			return errors.New("unexpected Gateway storage result")
		}
		return fn(ctx, tx, row)
	})
}
func audit(ctx context.Context, tx storage.Storage, row model.ServiceAccount, actor, action, outcome string) error {
	id, err := ksuid.NewRandom()
	if err != nil {
		return err
	}
	return tx.Create(ctx, "ServiceAccountAudit", model.ServiceAccountAudit{Meta: model.Meta{ID: id.String()}, ServiceAccountID: row.ID, GatewayID: row.GatewayID, ActorUserID: actor, CreatorUserID: row.CreatedByUserID, Action: action, Outcome: outcome, Role: row.Role, ExpiresAt: row.ExpiresAt})
}
func save(ctx context.Context, tx storage.Storage, row *model.ServiceAccount) error {
	if err := tx.Replace(ctx, "ServiceAccount", row.ID, *row); err != nil {
		return err
	}
	stored, err := tx.Get(ctx, "ServiceAccount", row.ID)
	if err != nil {
		return err
	}
	value, ok := stored.(model.ServiceAccount)
	if !ok {
		return errors.New("unexpected service-account storage result")
	}
	*row = value
	return nil
}
func account(ctx context.Context, tx storage.Storage, gatewayID, id string) (model.ServiceAccount, error) {
	if !validID(id) {
		return model.ServiceAccount{}, storage.ErrNotFound
	}
	stored, err := tx.Get(ctx, "ServiceAccount", id)
	if err != nil {
		return model.ServiceAccount{}, err
	}
	row, ok := stored.(model.ServiceAccount)
	if !ok {
		return row, errors.New("unexpected service-account storage result")
	}
	if row.GatewayID != gatewayID {
		return model.ServiceAccount{}, storage.ErrNotFound
	}
	return row, nil
}
func oidcConnection(row model.Gateway, clientID string, ready bool) (Connection, error) {
	row = row.CurrentObservations()
	if ready && (row.Phase == nil || !strings.EqualFold(*row.Phase, "Running") || row.Status == nil || !strings.EqualFold(*row.Status, "Healthy")) {
		return Connection{}, ErrNotReady
	}
	config, err := parseOIDC(row.Oidc)
	if err != nil {
		return Connection{}, ErrNotReady
	}
	issuer, err := url.Parse(config.Issuer)
	if err != nil || issuer.Scheme != "https" || issuer.Hostname() == "" || issuer.User != nil || issuer.RawQuery != "" || issuer.Fragment != "" || config.ClientID == "" || !validText(config.ClientID, 255) || config.Audience != config.ClientID {
		return Connection{}, ErrNotReady
	}
	endpoint := ""
	if row.RouteAddress != nil {
		endpoint = strings.TrimSpace(*row.RouteAddress)
		if strings.HasPrefix(endpoint, "grpcs://") {
			endpoint = "https://" + strings.TrimPrefix(endpoint, "grpcs://")
		}
		if endpoint != "" {
			parsed, err := url.Parse(endpoint)
			if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
				return Connection{}, ErrNotReady
			}
		}
	}
	return Connection{GatewayName: row.Name, GatewayEndpoint: endpoint, Issuer: config.Issuer, TokenEndpoint: strings.TrimRight(config.Issuer, "/") + "/protocol/openid-connect/token", GrantType: "client_credentials", ClientID: clientID, Audience: config.Audience, AccessTokenLifetimeSeconds: 300}, nil
}
func (s *Service) Create(ctx context.Context, p gateways.Principal, gatewayID string, input CreateRequest) (Created, error) {
	var row model.ServiceAccount
	var connection Connection
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || !validText(input.Name, 128) || input.Description != nil && !validText(*input.Description, 1024) {
		return Created{}, gateways.ErrInvalid
	}
	if input.CredentialType != "" && input.CredentialType != "client_secret" {
		return Created{}, gateways.ErrInvalid
	}
	if input.Role == "" {
		input.Role = RoleUser
	}
	if input.Role != RoleUser && input.Role != RoleAdmin {
		return Created{}, gateways.ErrInvalid
	}
	now := s.now().UTC()
	expires := now.Add(DefaultExpiration)
	if input.ExpiresAt != nil {
		var err error
		expires, err = time.Parse(time.RFC3339Nano, *input.ExpiresAt)
		if err != nil || expires.Sub(now) < MinimumExpiration || expires.Sub(now) > MaximumExpiration {
			return Created{}, gateways.ErrInvalid
		}
	}
	err := s.locked(ctx, gatewayID, func(ctx context.Context, tx storage.Transaction, gateway model.Gateway) error {
		access, err := resolveAccess(ctx, tx, p, gatewayID)
		if err != nil {
			return err
		}
		if !allowed(access, input.Role) {
			return ErrRole
		}
		if s.provider == nil {
			return ErrUnavailable
		}
		connection, err = oidcConnection(gateway, "", true)
		if err != nil {
			return err
		}
		for _, creator := range []string{access.UserID, ""} {
			filters := map[string]string{"active": "true"}
			limit := int64(100)
			if creator != "" {
				filters["created_by_user_id"] = creator
				limit = 10
			}
			result, err := tx.List(ctx, "ServiceAccount", "gateway_id", gatewayID, storage.ListOptions{Page: 1, Size: 0, CountOnly: true, ImplicitFilters: filters})
			if err != nil {
				return err
			}
			if result.Total >= limit {
				if creator != "" {
					return ErrCreatorQuota
				}
				return ErrGatewayQuota
			}
		}
		id, err := ksuid.NewRandom()
		if err != nil {
			return err
		}
		name := strings.ToLower(input.Name)
		row = model.ServiceAccount{Meta: model.Meta{ID: id.String()}, GatewayID: gatewayID, ActiveName: &name, Name: input.Name, Description: input.Description, CredentialType: "client_secret", Role: input.Role, Status: "provisioning", CreatedByUserID: access.UserID, ClientID: fmt.Sprintf("hs-sa-%s-%s", gatewayID, id.String()), ExpiresAt: expires.UTC(), Active: true}
		if err := tx.Create(ctx, "ServiceAccount", row); err != nil {
			return err
		}
		return audit(ctx, tx, row, access.UserID, "create", "started")
	})
	if err != nil {
		return Created{}, err
	}
	// The reservation commits before the external call. A lost response or process
	// leaves stable IDs for recovery. No secret crosses the storage boundary.
	var credential Credential
	var resultErr error
	err = s.locked(ctx, gatewayID, func(ctx context.Context, tx storage.Transaction, gateway model.Gateway) error {
		var err error
		row, err = account(ctx, tx, gatewayID, row.ID)
		if err != nil {
			return err
		}
		if row.Status != "provisioning" {
			return ErrBusy
		}
		access, accessErr := resolveAccess(ctx, tx, p, gatewayID)
		connection, err = oidcConnection(gateway, row.ClientID, true)
		if accessErr != nil || !allowed(access, input.Role) || err != nil {
			resultErr = ErrRole
			return failCreation(ctx, tx, &row, "creator_access_changed")
		}
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		credential, err = s.provider.Provision(call, Spec{ClientID: row.ClientID, DisplayName: row.Name, GatewayClientID: connection.Audience, GatewayID: gatewayID, ServiceAccountID: row.ID, CreatorUserID: row.CreatedByUserID, Role: row.Role, ExpectedIssuer: connection.Issuer, AccessTokenLifetimeSeconds: 300})
		cancel()
		if err != nil || credential.ClientID != row.ClientID || credential.ClientUUID == "" || !validText(credential.ClientUUID, 255) || credential.Subject == "" || !validText(credential.Subject, 255) || credential.Secret == "" || !validText(credential.Secret, 8192) {
			resultErr = ErrUnavailable
			credential = Credential{}
			return failCreation(ctx, tx, &row, "provisioning_failed")
		}
		row.ClientUuid = credential.ClientUUID
		row.Subject = credential.Subject
		// The grant can change while the provider is working. Read it again.
		access, err = resolveAccess(ctx, tx, p, gatewayID)
		if err != nil || !allowed(access, row.Role) {
			resultErr = ErrRole
			credential = Credential{}
			return failCreation(ctx, tx, &row, "creator_access_changed")
		}
		row.Status = "ready"
		row.LastError = nil
		if err := save(ctx, tx, &row); err != nil {
			return err
		}
		return audit(ctx, tx, row, row.CreatedByUserID, "create", "succeeded")
	})
	if err != nil || resultErr != nil {
		// Cleanup uses stable resource IDs even if the provider reply was lost.
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		defer cancel()
		_ = s.locked(cleanup, gatewayID, func(ctx context.Context, tx storage.Transaction, _ model.Gateway) error {
			current, err := account(ctx, tx, gatewayID, row.ID)
			if err != nil {
				return err
			}
			if current.Status != "provisioning" && current.Status != "ready" {
				return nil
			}
			return failCreation(ctx, tx, &current, "credential_not_delivered")
		})
		_ = s.Recover(cleanup, gatewayID, row.ID)
		return Created{}, errors.Join(err, resultErr)
	}
	return Created{Account: row, Connection: connection, Secret: credential.Secret}, nil
}
func failCreation(ctx context.Context, tx storage.Transaction, row *model.ServiceAccount, reason string) error {
	row.Status = "error"
	row.LastError = &reason
	if err := save(ctx, tx, row); err != nil {
		return err
	}
	return audit(ctx, tx, *row, row.CreatedByUserID, "create", "failed")
}

func (s *Service) Get(ctx context.Context, p gateways.Principal, gatewayID, id string) (model.ServiceAccount, Connection, error) {
	var row model.ServiceAccount
	var connection Connection
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		gateway, err := tx.Get(ctx, "Gateway", gatewayID)
		if err != nil {
			return err
		}
		access, err := resolveAccess(ctx, tx, p, gatewayID)
		if err != nil {
			return err
		}
		row, err = account(ctx, tx, gatewayID, id)
		if err != nil {
			return err
		}
		if !access.Owner && row.CreatedByUserID != access.UserID {
			return storage.ErrNotFound
		}
		value, ok := gateway.(model.Gateway)
		if !ok {
			return errors.New("unexpected Gateway storage result")
		}
		connection, err = oidcConnection(value, row.ClientID, false)
		return err
	})
	if err != nil {
		return model.ServiceAccount{}, Connection{}, err
	}
	return row, connection, nil
}
func (s *Service) List(ctx context.Context, p gateways.Principal, gatewayID string, page, size int) (storage.ListResult, Access, error) {
	return s.ListQuery(ctx, p, gatewayID, ListOptions{Page: page, Size: size})
}

func (s *Service) ListQuery(ctx context.Context, p gateways.Principal, gatewayID string, options ListOptions) (storage.ListResult, Access, error) {
	var result storage.ListResult
	var access Access
	if !validID(gatewayID) {
		return result, access, storage.ErrNotFound
	}
	column, direction, err := options.validate()
	if err != nil {
		return result, access, gateways.ErrInvalid
	}
	err = s.repository.WithTransaction(ctx, func(ctx context.Context, tx storage.Transaction) error {
		if _, err := tx.Get(ctx, "Gateway", gatewayID); err != nil {
			return err
		}
		var err error
		access, err = resolveAccess(ctx, tx, p, gatewayID)
		if err != nil {
			return err
		}
		opts := storage.ListOptions{Page: options.Page, Size: options.Size, OrderBy: []storage.OrderByField{{Field: column, Direction: direction}, {Field: "id", Direction: direction}}, ImplicitFilters: map[string]string{}}
		if options.Status != "" {
			opts.ImplicitFilters["status"] = options.Status
		}
		if options.Search != "" {
			opts.Filter = &storage.RowFilter{Text: &storage.TextMatch{Fields: []string{"name", "client_id", "subject"}, Value: options.Search}}
		}
		if !access.Owner {
			opts.ImplicitFilters["created_by_user_id"] = access.UserID
		}
		result, err = tx.List(ctx, "ServiceAccount", "gateway_id", gatewayID, opts)
		return err
	})
	return result, access, err
}

// Change stores the requested terminal action before contacting the provider.
// False means the durable request still needs recovery. It never re-enables an account.
func (s *Service) Change(ctx context.Context, p gateways.Principal, gatewayID, id string, remove bool) (model.ServiceAccount, bool, error) {
	var row model.ServiceAccount
	action, target := "revoke", "revoking"
	if remove {
		action, target = "delete", "deleting"
	}
	err := s.locked(ctx, gatewayID, func(ctx context.Context, tx storage.Transaction, _ model.Gateway) error {
		access, err := resolveAccess(ctx, tx, p, gatewayID)
		if err != nil {
			return err
		}
		row, err = account(ctx, tx, gatewayID, id)
		if err != nil {
			return err
		}
		if !access.Owner && row.CreatedByUserID != access.UserID {
			return storage.ErrNotFound
		}
		if row.Status == "provisioning" && s.now().Before(row.CreatedTime.Add(ReclaimAfter)) {
			return ErrBusy
		}
		if row.Status == "deleting" && !remove {
			return ErrBusy
		}
		if !remove && (row.Status == "revoked" || row.Status == "expired") {
			return nil
		}
		row.Status = target
		row.LastError = nil
		if err := save(ctx, tx, &row); err != nil {
			return err
		}
		return audit(ctx, tx, row, access.UserID, action, "started")
	})
	if err != nil {
		return model.ServiceAccount{}, false, err
	}
	if s.provider == nil {
		return row, false, nil
	}
	err = s.Recover(ctx, gatewayID, id)
	if err != nil {
		return row, false, nil
	}
	if remove {
		return row, true, nil
	}
	stored, err := s.repository.Get(ctx, "ServiceAccount", id)
	if err != nil {
		return row, false, err
	}
	value, ok := stored.(model.ServiceAccount)
	if !ok {
		return row, false, errors.New("unexpected service-account storage result")
	}
	return value, true, nil
}

// Recover enforces terminal actions and the creator's current role ceiling.
// It never creates a client, raises its role, or returns a secret.
func (s *Service) Recover(ctx context.Context, gatewayID, id string) error {
	if s.provider == nil {
		return ErrUnavailable
	}
	pending := true
	// Store a pending state before any external role or expiry action.
	err := s.locked(ctx, gatewayID, func(ctx context.Context, tx storage.Transaction, _ model.Gateway) error {
		row, err := account(ctx, tx, gatewayID, id)
		if errors.Is(err, storage.ErrNotFound) {
			pending = false
			return nil
		}
		if err != nil {
			return err
		}
		if row.Status == "degraded" && row.Role == RoleAdmin {
			// Earlier versions did not store the lower role with pending state.
			row.Role = RoleUser
			return save(ctx, tx, &row)
		}
		if row.Status != "ready" {
			return nil
		}
		access, err := accessForUser(ctx, tx, gatewayID, row.CreatedByUserID)
		if err != nil && !errors.Is(err, storage.ErrNotFound) {
			return err
		}
		if err != nil || !s.now().Before(row.ExpiresAt) {
			row.Status = "revoking"
		} else if row.Role == RoleAdmin && !access.Owner {
			row.Status = "degraded"
			row.Role = RoleUser
		} else {
			pending = false
			return nil
		}
		if err := save(ctx, tx, &row); err != nil {
			return err
		}
		return audit(ctx, tx, row, "system", "reconcile", "started")
	})
	if err != nil || !pending {
		return err
	}
	queuedRevocation := false
	err = s.locked(ctx, gatewayID, func(ctx context.Context, tx storage.Transaction, gateway model.Gateway) error {
		row, err := account(ctx, tx, gatewayID, id)
		if errors.Is(err, storage.ErrNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		remove := false
		switch row.Status {
		case "error", "deleting":
			remove = true
		case "provisioning":
			if s.now().Before(row.CreatedTime.Add(ReclaimAfter)) {
				return ErrBusy
			}
			remove = true
		case "revoking", "revoked", "expired":
		case "ready":
			return nil
		case "degraded":
			access, accessErr := accessForUser(ctx, tx, gatewayID, row.CreatedByUserID)
			if accessErr != nil && !errors.Is(accessErr, storage.ErrNotFound) {
				return accessErr
			}
			if accessErr == nil && s.now().Before(row.ExpiresAt) {
				desired := row.Role
				if !access.Owner {
					desired = RoleUser
				}
				connection, err := oidcConnection(gateway, row.ClientID, false)
				if err == nil {
					call, cancel := context.WithTimeout(ctx, 5*time.Second)
					err = s.provider.Reconcile(call, Spec{ClientID: row.ClientID, DisplayName: row.Name, GatewayClientID: connection.Audience, GatewayID: gatewayID, ServiceAccountID: row.ID, CreatorUserID: row.CreatedByUserID, Role: desired, ExpectedIssuer: connection.Issuer, AccessTokenLifetimeSeconds: 300}, row.ClientUuid, row.Subject)
					cancel()
				}
				if err != nil {
					// An invalid identity or failed role reduction must not preserve
					// the old credential. Commit terminal intent before provider cleanup.
					row.Status = "revoking"
					if err := save(ctx, tx, &row); err != nil {
						return err
					}
					queuedRevocation = true
					return audit(ctx, tx, row, "system", "revoke", "started")
				}
				row.Role = desired
				row.Status = "ready"
				row.LastError = nil
				if err := save(ctx, tx, &row); err != nil {
					return err
				}
				return audit(ctx, tx, row, "system", "reconcile", "succeeded")
			}
		default:
			return errors.New("unknown service-account state")
		}
		call, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if remove {
			err = s.provider.Delete(call, row.GatewayID, row.ID, row.ClientUuid)
		} else {
			err = s.provider.Revoke(call, row.GatewayID, row.ID, row.ClientUuid)
		}
		if err != nil {
			return ErrUnavailable
		}
		row.Active = false
		row.ActiveName = nil
		row.LastError = nil
		action := "revoke"
		if remove {
			action = "delete"
		} else {
			now := s.now().UTC()
			if row.RevokedAt == nil {
				row.RevokedAt = &now
			}
			if !s.now().Before(row.ExpiresAt) {
				row.Status = "expired"
			} else {
				row.Status = "revoked"
			}
		}
		if err := save(ctx, tx, &row); err != nil {
			return err
		}
		if err := audit(ctx, tx, row, "system", action, "succeeded"); err != nil {
			return err
		}
		if remove {
			return tx.Delete(ctx, "ServiceAccount", row.ID)
		}
		return nil
	})
	if err == nil && queuedRevocation {
		return s.Recover(ctx, gatewayID, id)
	}
	return err
}

// Diagnostic formatting must not expose credentials.
func (Credential) String() string   { return "[credential omitted]" }
func (Credential) GoString() string { return "[credential omitted]" }
func (Credential) MarshalJSON() ([]byte, error) {
	return nil, errors.New("credential serialization is not permitted")
}
func (Created) String() string   { return "[creation result omitted]" }
func (Created) GoString() string { return "[creation result omitted]" }
