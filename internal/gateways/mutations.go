package gateways

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jsell-rh/hypershell-stego/internal/resourceevents"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

// PatchRequest contains the public patch fields. Nil leaves a field unchanged.
// An empty DNS list also leaves that field unchanged, as in the reference API.
// DatabaseID is accepted for compatibility and does not change placement.
type PatchRequest struct {
	Name             *string  `json:"name,omitempty"`
	ClusterID        *string  `json:"cluster_id,omitempty"`
	ReleaseID        *string  `json:"release_id,omitempty"`
	DatabaseID       *string  `json:"database_id,omitempty"`
	ExternalDNS      *string  `json:"external_dns,omitempty"`
	TLSMode          *string  `json:"tls_mode,omitempty"`
	ServiceType      *string  `json:"service_type,omitempty"`
	Status           *string  `json:"status,omitempty"`
	Phase            *string  `json:"phase,omitempty"`
	Image            *string  `json:"image,omitempty"`
	SupervisorImage  *string  `json:"supervisor_image,omitempty"`
	ServerDNSNames   []string `json:"server_dns_names,omitempty"`
	RouteAddress     *string  `json:"route_address,omitempty"`
	OIDC             *string  `json:"oidc,omitempty"`
	Route            *string  `json:"route,omitempty"`
	CredentialDriver *string  `json:"credential_driver,omitempty"`
}

func (s *Service) Update(ctx context.Context, p Principal, id string, patch PatchRequest) (model.Gateway, error) {
	if err := validatePrincipal(p); err != nil {
		return model.Gateway{}, err
	}
	if s.isControlPlane(p) {
		return model.Gateway{}, ErrObservationRequired
	}
	if patch.Phase != nil || patch.Status != nil {
		return model.Gateway{}, ErrObservationOwned
	}
	return s.update(ctx, p, id, patch, nil, 0)
}

var ErrObservationRequired = errors.New("controller write requires an observed resource version")
var ErrObservationOwned = errors.New("phase and status are controller-owned fields")

// UpdateControlPlane requires the revision read before external work.
func (s *Service) UpdateControlPlane(ctx context.Context, p Principal, id string, patch PatchRequest, consoleAddress *string, version int64) (model.Gateway, error) {
	if err := validatePrincipal(p); err != nil {
		return model.Gateway{}, err
	}
	if !s.isControlPlane(p) {
		return model.Gateway{}, ErrForbidden
	}
	if version < 1 {
		return model.Gateway{}, ErrObservationRequired
	}
	return s.update(ctx, p, id, patch, consoleAddress, version)
}

func (s *Service) update(ctx context.Context, p Principal, id string, patch PatchRequest, consoleAddress *string, version int64) (model.Gateway, error) {
	var row model.Gateway
	if err := validatePrincipal(p); err != nil {
		return row, err
	}
	if !validID(id) {
		return row, store.ErrNotFound
	}
	err := s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		current, err := s.mutationTarget(ctx, tx, p, id, false)
		if err != nil {
			return err
		}
		if version > 0 {
			if err := s.authorizeControllerWrite(p, current.ClusterID, patch, consoleAddress); err != nil {
				return err
			}
		}
		if err := applyPatch(&current, patch, consoleAddress); err != nil {
			return err
		}
		for entity, reference := range map[string]*string{"ManagedCluster": patch.ClusterID, "GatewayRelease": patch.ReleaseID} {
			if reference != nil {
				if _, err := tx.Get(ctx, entity, *reference); err != nil {
					if errors.Is(err, store.ErrNotFound) {
						return ErrInvalid
					}
					return err
				}
			}
		}
		if patch.Phase != nil || patch.Status != nil {
			writer, ok := tx.(store.ObservationWriter)
			if !ok {
				return errors.New("Gateway storage does not support observations")
			}
			err = writer.ObserveIfVersion(ctx, "Gateway", id, version, "workload", map[string]any{"phase": *patch.Phase, "status": *patch.Status})
		} else if version > 0 {
			writer, ok := tx.(store.VersionedWriter)
			if !ok {
				return errors.New("Gateway storage does not support conditional writes")
			}
			err = writer.ReplaceIfVersion(ctx, "Gateway", id, version, current)
		} else {
			err = tx.Replace(ctx, "Gateway", id, current)
		}
		if err != nil {
			return err
		}
		stored, err := tx.Get(ctx, "Gateway", id)
		if err != nil {
			return err
		}
		var ok bool
		row, ok = stored.(model.Gateway)
		if !ok {
			return errors.New("unexpected Gateway storage result")
		}
		return notifyGateway(tx, id, "Update", "gateway.updated")
	})
	if err != nil {
		return model.Gateway{}, err
	}
	return row, nil
}

// Delete locks the Gateway against concurrent service-account reservations.
// A service without a cleaner refuses live account metadata. Configured APIs
// remove provider identities before they commit metadata and Gateway deletion.
var ErrGatewayCleanupUnavailable = errors.New("Gateway service-account cleanup is unavailable")

var ErrServiceAccountsExist = errors.New("service accounts require cleanup before Gateway deletion")

func (s *Service) Delete(ctx context.Context, p Principal, id string) error {
	if err := validatePrincipal(p); err != nil {
		return err
	}
	if !validID(id) {
		return store.ErrNotFound
	}
	return s.repository.WithLockedResource(ctx, "Gateway", "id", id, func(ctx context.Context, tx store.Transaction, _ any) error {
		if _, err := s.mutationTarget(ctx, tx, p, id, true); err != nil {
			return err
		}
		if s.accountCleaner != nil {
			if err := s.accountCleaner.CleanupGateway(ctx, tx, id); err != nil {
				return err
			}
		} else {
			accounts, err := tx.List(ctx, "ServiceAccount", "gateway_id", id, store.ListOptions{Page: 1, Size: 0, CountOnly: true})
			if err != nil {
				return err
			}
			if accounts.Total != 0 {
				return ErrServiceAccountsExist
			}
		}
		if err := tx.Delete(ctx, "Gateway", id); err != nil {
			return err
		}
		return notifyGateway(tx, id, "Delete", "gateway.deleted")
	})
}

// The access check and mutation share the same serializable transaction.
func (s *Service) mutationTarget(ctx context.Context, tx store.Transaction, p Principal, id string, allowAdmin bool) (model.Gateway, error) {
	user, err := syncUser(ctx, tx, p)
	if err != nil {
		return model.Gateway{}, err
	}
	opts := store.ListOptions{Page: 1, Size: 1}
	if !s.isControlPlane(p) && !(allowAdmin && slices.Contains(p.Roles, "platform:admin")) {
		role, err := findRole(ctx, tx, "gateway:owner")
		if err != nil {
			return model.Gateway{}, err
		}
		opts.Related = []store.RelatedFilter{{Entity: "RoleBinding", ForeignField: "gateway_id", Values: map[string][]string{"user_id": {user.ID}, "role_id": {role.ID}, "scope": {"gateway"}}}}
	}
	result, err := tx.List(ctx, "Gateway", "id", id, opts)
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

func applyPatch(row *model.Gateway, p PatchRequest, consoleAddress *string) error {
	if p.CredentialDriver != nil && row.CredentialDriver != nil && *row.CredentialDriver != "" && *row.CredentialDriver != *p.CredentialDriver {
		return store.ErrConflict
	}
	if p.Name != nil {
		row.Name = *p.Name
	}
	if p.ClusterID != nil {
		row.ClusterID = *p.ClusterID
	}
	if p.ReleaseID != nil {
		row.ReleaseID = *p.ReleaseID
	}
	fields := []struct {
		source *string
		target **string
	}{
		{p.ExternalDNS, &row.ExternalDns}, {p.TLSMode, &row.TlsMode}, {p.ServiceType, &row.ServiceType},
		{p.Status, &row.Status}, {p.Phase, &row.Phase}, {p.Image, &row.Image}, {p.SupervisorImage, &row.SupervisorImage},
		{p.RouteAddress, &row.RouteAddress}, {p.OIDC, &row.Oidc}, {p.Route, &row.Route}, {p.CredentialDriver, &row.CredentialDriver}, {consoleAddress, &row.ConsoleAddress},
	}
	for _, field := range fields {
		if field.source == nil {
			continue
		}
		if len(*field.source) > 8192 || !utf8.ValidString(*field.source) || strings.ContainsRune(*field.source, 0) {
			return ErrInvalid
		}
		value := *field.source
		*field.target = &value
	}
	var names []string
	if len(p.ServerDNSNames) > 0 {
		names = p.ServerDNSNames
	} else if len(row.ServerDnsNames) > 0 {
		if err := json.Unmarshal(row.ServerDnsNames, &names); err != nil {
			return err
		}
	}
	if err := validateCreate(CreateRequest{Name: row.Name, ClusterID: row.ClusterID, ReleaseID: row.ReleaseID, ServerDNSNames: names}); err != nil {
		return err
	}
	if len(p.ServerDNSNames) > 0 {
		encoded, err := json.Marshal(p.ServerDNSNames)
		if err != nil {
			return err
		}
		row.ServerDnsNames = encoded
	}
	return nil
}

// Events carry an identifier. Configuration and credentials stay in storage.
func notifyGateway(tx store.Transaction, id, eventType, kind string) error {
	return resourceevents.Notify(tx, "Gateways", id, eventType, kind)
}
