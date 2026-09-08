// Package gateways supplies Hypershell rules over generated storage contracts.
package gateways

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/google/uuid"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

var ErrForbidden = errors.New("request is forbidden")
var ErrInvalid = errors.New("request is invalid")
var ErrIdentity = errors.New("verified user identity is required")

// Principal must come from a verified access token. Platform roles apply to
// this request. Stored Gateway grants remain valid after creator-role removal.
type Principal struct {
	Subject, Username, Email, Name string
	Roles                          []string
}

// CreateRequest contains client fields. Namespace and ownership are absent.
// DatabaseID is a required API placeholder. Placement replaces its value.
type CreateRequest struct {
	Name             string   `json:"name"`
	ClusterID        string   `json:"cluster_id"`
	ReleaseID        string   `json:"release_id"`
	DatabaseID       string   `json:"database_id"`
	ExternalDNS      *string  `json:"external_dns,omitempty"`
	TLSMode          *string  `json:"tls_mode,omitempty"`
	ServiceType      *string  `json:"service_type,omitempty"`
	Status           *string  `json:"status,omitempty"`
	Phase            *string  `json:"phase,omitempty"`
	Image            *string  `json:"image,omitempty"`
	SupervisorImage  *string  `json:"supervisor_image,omitempty"`
	ServerDNSNames   []string `json:"server_dns_names,omitempty"`
	OIDC             *string  `json:"oidc,omitempty"`
	Route            *string  `json:"route,omitempty"`
	CredentialDriver *string  `json:"credential_driver,omitempty"`
}

type Repository interface {
	store.Storage
	store.Transactor
}
type Service struct{ repository Repository }

func New(repository Repository) (*Service, error) {
	if repository == nil {
		return nil, errors.New("Gateway service requires storage")
	}
	return &Service{repository: repository}, nil
}

// Create commits the resource, owner grant, and notification as one change.
func (s *Service) Create(ctx context.Context, principal Principal, request CreateRequest) (model.Gateway, error) {
	var gateway model.Gateway
	if err := validatePrincipal(principal); err != nil {
		return gateway, err
	}
	if !slices.Contains(principal.Roles, "gateway:creator") {
		return gateway, ErrForbidden
	}
	if err := validateCreate(request); err != nil {
		return gateway, err
	}
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		user, err := syncUser(ctx, tx, principal)
		if err != nil {
			return err
		}
		// CNPG placement requires exactly one live managed database.
		databases, err := tx.List(ctx, "ManagedDatabase", "", "", store.ListOptions{Page: 1, Size: 2})
		if err != nil {
			return err
		}
		rows, ok := databases.Items.([]model.ManagedDatabase)
		if !ok {
			return errors.New("unexpected database storage result")
		}
		if databases.Total != 1 || len(rows) != 1 {
			return fmt.Errorf("%w: zero or multiple managed databases", ErrInvalid)
		}
		for entity, id := range map[string]string{"ManagedCluster": request.ClusterID, "GatewayRelease": request.ReleaseID} {
			if _, err := tx.Get(ctx, entity, id); err != nil {
				if errors.Is(err, store.ErrNotFound) {
					return fmt.Errorf("%w: placement reference does not exist", ErrInvalid)
				}
				return err
			}
		}
		id, err := ksuid.NewRandom()
		if err != nil {
			return err
		}
		names, err := json.Marshal(request.ServerDNSNames)
		if err != nil {
			return err
		}
		gateway = model.Gateway{
			Meta: model.Meta{ID: id.String()}, Name: request.Name,
			ClusterID: request.ClusterID, ReleaseID: request.ReleaseID, DatabaseID: rows[0].ID,
			Namespace:   "openshell-" + hex.EncodeToString(id.Payload()[:8]),
			ExternalDns: request.ExternalDNS, TlsMode: request.TLSMode, ServiceType: request.ServiceType,
			Status: request.Status, Phase: request.Phase, Image: request.Image, SupervisorImage: request.SupervisorImage,
			ServerDnsNames: names, Oidc: request.OIDC, Route: request.Route, CredentialDriver: request.CredentialDriver,
		}
		if err := tx.Create(ctx, "Gateway", gateway); err != nil {
			return err
		}
		role, err := findRole(ctx, tx, "gateway:owner")
		if err != nil {
			return err
		}
		bindingID, err := ksuid.NewRandom()
		if err != nil {
			return err
		}
		if err := tx.Create(ctx, "RoleBinding", model.RoleBinding{Meta: model.Meta{ID: bindingID.String()}, UserID: user.ID, RoleID: role.ID, GatewayID: gateway.ID, Scope: "gateway"}); err != nil {
			return err
		}
		stored, err := tx.Get(ctx, "Gateway", gateway.ID)
		if err != nil {
			return err
		}
		gateway, ok = stored.(model.Gateway)
		if !ok {
			return errors.New("unexpected Gateway storage result")
		}
		// Events contain an identifier. Configuration and credentials stay in storage.
		payload, err := json.Marshal(struct {
			Source    string `json:"source"`
			SourceID  string `json:"source_id"`
			EventType string `json:"event_type"`
		}{"Gateways", gateway.ID, "Create"})
		if err != nil {
			return err
		}
		messageID, err := uuid.NewRandom()
		if err != nil {
			return err
		}
		return tx.Notify(store.Notification{ID: messageID, Destination: "kafka", ResourceKey: gateway.ID, Kind: "gateway.created", Payload: payload})
	})
	if err != nil {
		return model.Gateway{}, err
	}
	return gateway, nil
}

func (s *Service) Get(ctx context.Context, principal Principal, id string) (model.Gateway, error) {
	if err := validatePrincipal(principal); err != nil {
		return model.Gateway{}, err
	}
	if !validID(id) {
		return model.Gateway{}, store.ErrNotFound
	}
	result, err := s.list(ctx, principal, id, 1, 1)
	if err != nil {
		return model.Gateway{}, err
	}
	rows, ok := result.Items.([]model.Gateway)
	if !ok {
		return model.Gateway{}, errors.New("unexpected Gateway storage result")
	}
	if len(rows) != 1 {
		return model.Gateway{}, store.ErrNotFound
	}
	return rows[0], nil
}

func (s *Service) List(ctx context.Context, principal Principal, page, size int) (store.ListResult, error) {
	if page < 1 || size < 0 || size > 100 || page > 1000000 {
		return store.ListResult{}, ErrInvalid
	}
	return s.list(ctx, principal, "", page, size)
}

func (s *Service) list(ctx context.Context, principal Principal, id string, page, size int) (store.ListResult, error) {
	var result store.ListResult
	if err := validatePrincipal(principal); err != nil {
		return result, err
	}
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		user, err := syncUser(ctx, tx, principal)
		if err != nil {
			return err
		}
		opts := store.ListOptions{Page: page, Size: size, CountOnly: size == 0, OrderBy: []store.OrderByField{{Field: "id", Direction: "asc"}}}
		if !slices.Contains(principal.Roles, "platform:admin") {
			owner, err := findRole(ctx, tx, "gateway:owner")
			if err != nil {
				return err
			}
			viewer, err := findRole(ctx, tx, "gateway:viewer")
			if err != nil {
				return err
			}
			opts.Related = []store.RelatedFilter{{Entity: "RoleBinding", ForeignField: "gateway_id", Values: map[string][]string{"user_id": {user.ID}, "role_id": {owner.ID, viewer.ID}, "scope": {"gateway"}}}}
		}
		field := ""
		if id != "" {
			field = "id"
		}
		result, err = tx.List(ctx, "Gateway", field, id, opts)
		return err
	})
	return result, err
}

func findRole(ctx context.Context, storage store.Storage, name string) (model.Role, error) {
	result, err := storage.List(ctx, "Role", "name", name, store.ListOptions{Page: 1, Size: 1})
	if err != nil {
		return model.Role{}, err
	}
	rows, ok := result.Items.([]model.Role)
	if !ok || result.Total != 1 || len(rows) != 1 {
		return model.Role{}, errors.New("required role is absent")
	}
	return rows[0], nil
}

func syncUser(ctx context.Context, storage store.Storage, principal Principal) (model.User, error) {
	result, err := storage.List(ctx, "User", "username", principal.Username, store.ListOptions{Page: 1, Size: 1})
	if err != nil {
		return model.User{}, err
	}
	rows, ok := result.Items.([]model.User)
	if !ok {
		return model.User{}, errors.New("unexpected user storage result")
	}
	if len(rows) == 0 {
		id, err := ksuid.NewRandom()
		if err != nil {
			return model.User{}, err
		}
		user := model.User{Meta: model.Meta{ID: id.String()}, Username: principal.Username, Email: principal.Email, Name: principal.Name}
		return user, storage.Create(ctx, "User", user)
	}
	user := rows[0]
	if user.Email != principal.Email || user.Name != principal.Name {
		user.Email, user.Name = principal.Email, principal.Name
		if err := storage.Replace(ctx, "User", user.ID, user); err != nil {
			return model.User{}, err
		}
	}
	return user, nil
}
func validID(value string) bool {
	id, err := ksuid.Parse(value)
	return err == nil && id != ksuid.Nil && id.String() == value
}
func validatePrincipal(p Principal) error {
	if strings.TrimSpace(p.Subject) == "" || strings.TrimSpace(p.Username) == "" || len(p.Username) > 255 || len(p.Email) > 320 || len(p.Name) > 255 || !utf8.ValidString(p.Username+p.Email+p.Name) || strings.ContainsRune(p.Subject+p.Username+p.Email+p.Name, 0) {
		return ErrIdentity
	}
	return nil
}
func validateCreate(r CreateRequest) error {
	if strings.TrimSpace(r.Name) == "" || len(r.Name) > 255 || !utf8.ValidString(r.Name) || strings.ContainsRune(r.Name, 0) || !validID(r.ClusterID) || !validID(r.ReleaseID) || strings.TrimSpace(r.DatabaseID) == "" {
		return ErrInvalid
	}
	if len(r.ServerDNSNames) > 128 {
		return ErrInvalid
	}
	for _, v := range r.ServerDNSNames {
		if len(v) > 253 || !utf8.ValidString(v) || strings.ContainsRune(v, 0) {
			return ErrInvalid
		}
	}
	for _, v := range []*string{r.ExternalDNS, r.TLSMode, r.ServiceType, r.Status, r.Phase, r.Image, r.SupervisorImage, r.OIDC, r.Route, r.CredentialDriver} {
		if v != nil && (len(*v) > 8192 || !utf8.ValidString(*v) || strings.ContainsRune(*v, 0)) {
			return ErrInvalid
		}
	}
	return nil
}
