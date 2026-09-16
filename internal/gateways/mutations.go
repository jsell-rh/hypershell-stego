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
type PatchRequest struct {
	Name             *string  `json:"name,omitempty"`
	ClusterID        *string  `json:"cluster_id,omitempty"`
	ReleaseID        *string  `json:"release_id,omitempty"`
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
	return s.update(ctx, p, id, patch, nil, nil, 0)
}

var ErrObservationRequired = errors.New("controller write requires an observed resource version")
var ErrObservationOwned = errors.New("phase, status, and route_address are controller-owned fields")

// UpdateControlPlane requires the revision read before external work.
func (s *Service) UpdateControlPlane(ctx context.Context, p Principal, id string, patch PatchRequest, consoleAddress, routeAddress *string, version int64) (model.Gateway, error) {
	if err := validatePrincipal(p); err != nil {
		return model.Gateway{}, err
	}
	if !s.isControlPlane(p) {
		return model.Gateway{}, ErrForbidden
	}
	if version < 1 {
		return model.Gateway{}, ErrObservationRequired
	}
	return s.update(ctx, p, id, patch, consoleAddress, routeAddress, version)
}

func (s *Service) update(ctx context.Context, p Principal, id string, patch PatchRequest, consoleAddress, routeAddress *string, version int64) (model.Gateway, error) {
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
			if err := s.authorizeControllerWrite(p, current.ClusterID, patch, consoleAddress, routeAddress); err != nil {
				return err
			}
		}
		previousClusterID := current.ClusterID
		if err := applyPatch(&current, patch, consoleAddress, routeAddress); err != nil {
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
		if current.ClusterID != previousClusterID {
			return store.ErrConflict
		}
		if patch.Phase != nil || routeAddress != nil {
			writer, ok := tx.(store.ObservationWriter)
			if !ok {
				return errors.New("Gateway storage does not support observations")
			}
			observationVersion := version
			if patch.Phase != nil {
				if err := writer.ObserveIfVersion(ctx, "Gateway", id, version, "workload", map[string]any{"phase": *patch.Phase, "status": *patch.Status}); err != nil {
					return err
				}
				if routeAddress != nil {
					// This is our own write in the same serializable transaction.
					// The first write checked the original external observation
					// revision and retains the row lock until both groups commit.
					value, err := tx.Get(ctx, "Gateway", id)
					if err != nil {
						return err
					}
					written, ok := value.(model.Gateway)
					if !ok || written.ResourceVersion <= version {
						return errors.New("Gateway observation revision is invalid")
					}
					observationVersion = written.ResourceVersion
				}
			}
			if routeAddress != nil {
				err = writer.ObserveIfVersion(ctx, "Gateway", id, observationVersion, "endpoint", map[string]any{"route_address": *routeAddress})
			}
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

// Delete commits a durable request. The soft deletion blocks new account
// reservations. Cleanup and final deletion run after this request returns.
var ErrGatewayCleanupUnavailable = errors.New("Gateway service-account cleanup is unavailable")
var ErrServiceAccountsExist = errors.New("service accounts require cleanup before Gateway deletion")

func (s *Service) Delete(ctx context.Context, p Principal, id string) error {
	if err := validatePrincipal(p); err != nil {
		return err
	}
	if !validID(id) {
		return store.ErrNotFound
	}
	return s.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		row, err := s.mutationTargetWithVisibility(ctx, tx, p, id, true, true)
		if err != nil {
			return err
		}
		if row.DeletedAt.Valid {
			return nil
		}
		if err := tx.Delete(ctx, "Gateway", id); err != nil {
			return err
		}
		return notifyGateway(tx, id, "Update", "gateway.updated")
	})
}

// The access check and mutation share the same serializable transaction.
func (s *Service) mutationTarget(ctx context.Context, tx store.Transaction, p Principal, id string, allowAdmin bool) (model.Gateway, error) {
	return s.mutationTargetWithVisibility(ctx, tx, p, id, allowAdmin, false)
}
func (s *Service) mutationTargetWithVisibility(ctx context.Context, tx store.Transaction, p Principal, id string, allowAdmin, includeDeleting bool) (model.Gateway, error) {
	user, err := syncUser(ctx, tx, p)
	if err != nil {
		return model.Gateway{}, err
	}
	opts := store.ListOptions{Page: 1, Size: 1, IncludeDeleting: includeDeleting}
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

func applyPatch(row *model.Gateway, p PatchRequest, consoleAddress, routeAddress *string) error {
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
		{routeAddress, &row.RouteAddress}, {p.OIDC, &row.Oidc}, {p.Route, &row.Route}, {p.CredentialDriver, &row.CredentialDriver}, {consoleAddress, &row.ConsoleAddress},
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
