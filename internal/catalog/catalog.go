// Package catalog supplies shared Hypershell records and their change events.
package catalog

import (
	"context"
	"errors"
	"slices"
	"strings"
	"unicode/utf8"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/resourceevents"
	store "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
	"github.com/segmentio/ksuid"
)

type Query struct {
	Page, Size int
	Search     string
	OrderBy    []store.OrderByField
}
type Service struct {
	Networks  *Resource[model.GatewayNetwork, NetworkCreate, NetworkPatch]
	Clusters  *Resource[model.ManagedCluster, ClusterCreate, ClusterPatch]
	Releases  *Resource[model.GatewayRelease, ReleaseCreate, ReleasePatch]
	Databases *Resource[model.ManagedDatabase, DatabaseCreate, DatabasePatch]
}

// Resource binds one catalog type to its storage and validation rules.
// Its fields are private so callers cannot change the selected entity or policy.
type Resource[T, C, P any] struct {
	repository                        store.Transactor
	authorize                         func(gateways.Principal, bool) error
	authorizeRecovery                 func(gateways.Principal) error
	authorizeCleanup                  func(gateways.Principal, string, string, string) error
	entity, foreignField, eventPrefix string
	create                            func(string, C) (T, error)
	patch                             func(*T, P) error
	requireControllerVersion          bool
	authorizeObservation              func(gateways.Principal, T, P) error
}

func New(repository store.Transactor, policy *gateways.Service) (*Service, error) {
	if repository == nil || policy == nil {
		return nil, errors.New("catalog requires storage and access rules")
	}
	return &Service{
		Networks:  &Resource[model.GatewayNetwork, NetworkCreate, NetworkPatch]{repository, policy.AuthorizeCatalog, policy.AuthorizeRecovery, policy.AuthorizeCleanup, "GatewayNetwork", "", "gatewaynetwork", newNetwork, patchNetwork, false, nil},
		Clusters:  &Resource[model.ManagedCluster, ClusterCreate, ClusterPatch]{repository, policy.AuthorizeCatalog, policy.AuthorizeRecovery, policy.AuthorizeCleanup, "ManagedCluster", "cluster_id", "managedcluster", newCluster, patchCluster, false, nil},
		Releases:  &Resource[model.GatewayRelease, ReleaseCreate, ReleasePatch]{repository, policy.AuthorizeCatalog, policy.AuthorizeRecovery, policy.AuthorizeCleanup, "GatewayRelease", "release_id", "gatewayrelease", newRelease, patchRelease, false, nil},
		Databases: &Resource[model.ManagedDatabase, DatabaseCreate, DatabasePatch]{repository, policy.AuthorizeCatalog, policy.AuthorizeRecovery, policy.AuthorizeCleanup, "ManagedDatabase", "database_id", "manageddatabase", newDatabase, patchDatabase, true, databaseObservationPolicy(policy)},
	}, nil
}
func validID(id string) bool {
	value, err := ksuid.Parse(id)
	return err == nil && value != ksuid.Nil && value.String() == id
}
func textField(s string, required bool, max int) bool {
	return (!required || strings.TrimSpace(s) != "") && len(s) <= max && utf8.ValidString(s) && !strings.ContainsRune(s, 0)
}

// DatabaseNamespace derives the immutable namespace from a canonical KSUID.
func DatabaseNamespace(id string) (string, error) {
	return gateways.DatabaseNamespace(id)
}
func (r *Resource[T, C, P]) Get(ctx context.Context, p gateways.Principal, id string) (T, error) {
	var row T
	if err := r.authorize(p, false); err != nil {
		return row, err
	}
	if !validID(id) {
		return row, store.ErrNotFound
	}
	err := r.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		value, err := tx.Get(ctx, r.entity, id)
		if err != nil {
			return err
		}
		var ok bool
		row, ok = value.(T)
		if !ok {
			return errors.New("unexpected catalog storage result")
		}
		return nil
	})
	return row, err
}

// GetRetained requires controller access and reads current deletion state.
func (r *Resource[T, C, P]) GetRetained(ctx context.Context, p gateways.Principal, id string) (T, error) {
	var row T
	if err := r.authorizeRecovery(p); err != nil {
		return row, err
	}
	if err := r.authorize(p, false); err != nil {
		return row, err
	}
	if !validID(id) {
		return row, store.ErrNotFound
	}
	err := r.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		reader, ok := tx.(store.RetainedReader)
		if !ok {
			return errors.New("catalog storage does not support retained reads")
		}
		value, err := reader.GetRetained(ctx, r.entity, id)
		if err != nil {
			return err
		}
		row, ok = value.(T)
		if !ok {
			return errors.New("unexpected catalog storage result")
		}
		return nil
	})
	return row, err
}

// ObserveCleanup records a controller observation and its event together.
func (r *Resource[T, C, P]) ObserveCleanup(ctx context.Context, p gateways.Principal, id string, version int64, owner string, complete bool) error {
	if err := r.authorizeRecovery(p); err != nil {
		return err
	}
	if err := r.authorize(p, true); err != nil {
		return err
	}
	if !validID(id) {
		return store.ErrNotFound
	}
	if version < 1 {
		return gateways.ErrObservationRequired
	}
	if r.entity != "ManagedDatabase" || owner != "provider" {
		return gateways.ErrForbidden
	}
	if err := r.authorizeCleanup(p, r.entity, owner, ""); err != nil {
		return err
	}
	return r.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		writer, ok := tx.(store.CleanupWriter)
		if !ok {
			return errors.New("catalog storage does not support cleanup observations")
		}
		if err := writer.ObserveCleanupIfVersion(ctx, r.entity, id, version, owner, complete); err != nil {
			return err
		}
		// A deletion notice requests another current-state check.
		return r.notify(tx, id, "Delete", "deleted")
	})
}
func (r *Resource[T, C, P]) List(ctx context.Context, p gateways.Principal, q Query) (store.ListResult, error) {
	if err := r.authorize(p, false); err != nil {
		return store.ListResult{}, err
	}
	if q.Page < 1 || q.Page > 1000000 || q.Size < 0 || q.Size > 500 {
		return store.ListResult{}, gateways.ErrInvalid
	}
	order := append([]store.OrderByField(nil), q.OrderBy...)
	if !slices.ContainsFunc(order, func(v store.OrderByField) bool { return v.Field == "id" }) {
		order = append(order, store.OrderByField{Field: "id", Direction: "asc"})
	}
	var result store.ListResult
	err := r.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		var err error
		result, err = tx.List(ctx, r.entity, "", "", store.ListOptions{Page: q.Page, Size: q.Size, CountOnly: q.Size == 0, Search: q.Search, OrderBy: order})
		return err
	})
	return result, err
}
func (r *Resource[T, C, P]) notify(tx store.Transaction, id, operation, suffix string) error {
	return resourceevents.Notify(tx, r.entity+"s", id, operation, r.eventPrefix+"."+suffix)
}
func (r *Resource[T, C, P]) Create(ctx context.Context, p gateways.Principal, input C) (T, error) {
	var zero T
	if err := r.authorize(p, true); err != nil {
		return zero, err
	}
	key, err := ksuid.NewRandom()
	if err != nil {
		return zero, err
	}
	id := key.String()
	row, err := r.create(id, input)
	if err != nil {
		return zero, err
	}
	err = r.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		if err := tx.Create(ctx, r.entity, row); err != nil {
			return err
		}
		value, err := tx.Get(ctx, r.entity, id)
		if err != nil {
			return err
		}
		var ok bool
		row, ok = value.(T)
		if !ok {
			return errors.New("unexpected catalog storage result")
		}
		return r.notify(tx, id, "Create", "created")
	})
	if err != nil {
		return zero, err
	}
	return row, nil
}
func (r *Resource[T, C, P]) Update(ctx context.Context, p gateways.Principal, id string, input P) (T, error) {
	return r.update(ctx, p, id, input, 0)
}

// UpdateIfVersion binds a controller result to the revision read before its work.
func (r *Resource[T, C, P]) UpdateIfVersion(ctx context.Context, p gateways.Principal, id string, input P, version int64) (T, error) {
	var zero T
	if err := r.authorizeRecovery(p); err != nil {
		return zero, err
	}
	if !r.requireControllerVersion {
		return zero, gateways.ErrInvalid
	}
	if version < 1 {
		return zero, gateways.ErrObservationRequired
	}
	return r.update(ctx, p, id, input, version)
}

func (r *Resource[T, C, P]) update(ctx context.Context, p gateways.Principal, id string, input P, version int64) (T, error) {
	var row T
	if err := r.authorize(p, true); err != nil {
		return row, err
	}
	if r.requireControllerVersion && version == 0 && r.authorizeRecovery(p) == nil {
		return row, gateways.ErrObservationRequired
	}
	if !validID(id) {
		return row, store.ErrNotFound
	}
	err := r.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		value, err := tx.Get(ctx, r.entity, id)
		if err != nil {
			return err
		}
		var ok bool
		row, ok = value.(T)
		if !ok {
			return errors.New("unexpected catalog storage result")
		}
		if version > 0 {
			if r.authorizeObservation == nil {
				return gateways.ErrForbidden
			}
			if err := r.authorizeObservation(p, row, input); err != nil {
				return err
			}
		}
		if err := r.patch(&row, input); err != nil {
			return err
		}
		if version > 0 {
			writer, ok := tx.(store.VersionedWriter)
			if !ok {
				return errors.New("catalog storage does not support conditional writes")
			}
			if err := writer.ReplaceIfVersion(ctx, r.entity, id, version, row); err != nil {
				return err
			}
		} else if err := tx.Replace(ctx, r.entity, id, row); err != nil {
			return err
		}
		value, err = tx.Get(ctx, r.entity, id)
		if err != nil {
			return err
		}
		row, ok = value.(T)
		if !ok {
			return errors.New("unexpected catalog storage result")
		}
		return r.notify(tx, id, "Update", "updated")
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return row, nil
}

// Delete checks live Gateway references and removes the record in one serializable
// transaction. A concurrent Gateway change must commit before it or return a conflict.
func (r *Resource[T, C, P]) Delete(ctx context.Context, p gateways.Principal, id string) error {
	if err := r.authorize(p, true); err != nil {
		return err
	}
	if !validID(id) {
		return store.ErrNotFound
	}
	return r.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		if _, err := tx.Get(ctx, r.entity, id); err != nil {
			return err
		}
		if r.foreignField != "" {
			refs, err := tx.List(ctx, "Gateway", r.foreignField, id, store.ListOptions{Page: 1, Size: 0, CountOnly: true})
			if err != nil {
				return err
			}
			if refs.Total != 0 {
				return store.ErrConflict
			}
		}
		if err := tx.Delete(ctx, r.entity, id); err != nil {
			return err
		}
		return r.notify(tx, id, "Delete", "deleted")
	})
}

// Event reads current committed state. A delete notice is valid only for a deleted row.
func (r *Resource[T, C, P]) Event(ctx context.Context, p gateways.Principal, id string, deleted bool) (T, error) {
	var row T
	if err := r.authorize(p, false); err != nil {
		return row, err
	}
	if !validID(id) {
		return row, store.ErrNotFound
	}
	err := r.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		result, err := tx.List(ctx, r.entity, "id", id, store.ListOptions{Page: 1, Size: 1, IncludeDeleted: deleted})
		if err != nil {
			return err
		}
		rows, ok := result.Items.([]T)
		if !ok {
			return errors.New("unexpected catalog storage result")
		}
		if len(rows) != 1 {
			return store.ErrNotFound
		}
		row = rows[0]
		var gone bool
		switch v := any(row).(type) {
		case model.GatewayNetwork:
			gone = v.DeletedAt.Valid
		case model.ManagedCluster:
			gone = v.DeletedAt.Valid
		case model.GatewayRelease:
			gone = v.DeletedAt.Valid
		case model.ManagedDatabase:
			gone = v.DeletedAt.Valid
		default:
			return errors.New("unexpected catalog type")
		}
		if gone != deleted {
			return store.ErrNotFound
		}
		return nil
	})
	return row, err
}

type ClusterCreate struct {
	Name             string  `json:"name,omitempty"`
	Provider         string  `json:"provider,omitempty"`
	Region           *string `json:"region,omitempty"`
	KubeconfigSecret string  `json:"kubeconfig_secret,omitempty"`
	Status           *string `json:"status,omitempty"`
	ApiServerUrl     *string `json:"api_server_url,omitempty"`
}

type ClusterPatch struct {
	Name             *string `json:"name,omitempty"`
	Provider         *string `json:"provider,omitempty"`
	Region           *string `json:"region,omitempty"`
	KubeconfigSecret *string `json:"kubeconfig_secret,omitempty"`
	Status           *string `json:"status,omitempty"`
	ApiServerUrl     *string `json:"api_server_url,omitempty"`
}

func newCluster(id string, input ClusterCreate) (model.ManagedCluster, error) {
	row := model.ManagedCluster{Meta: model.Meta{ID: id}, Name: input.Name, Provider: input.Provider, Region: input.Region, KubeconfigSecret: input.KubeconfigSecret, Status: input.Status, ApiServerUrl: input.ApiServerUrl}
	return row, validateCluster(row)
}
func patchCluster(row *model.ManagedCluster, input ClusterPatch) error {
	if input.Name != nil {
		row.Name = *input.Name
	}
	if input.Provider != nil {
		row.Provider = *input.Provider
	}
	if input.Region != nil {
		row.Region = input.Region
	}
	if input.KubeconfigSecret != nil {
		row.KubeconfigSecret = *input.KubeconfigSecret
	}
	if input.Status != nil {
		row.Status = input.Status
	}
	if input.ApiServerUrl != nil {
		row.ApiServerUrl = input.ApiServerUrl
	}
	return validateCluster(*row)
}
func validateCluster(row model.ManagedCluster) error {
	if !textField(row.Name, true, 255) {
		return gateways.ErrInvalid
	}
	if !textField(row.Provider, true, 64) {
		return gateways.ErrInvalid
	}
	if row.Region != nil && !textField(*row.Region, false, 255) {
		return gateways.ErrInvalid
	}
	if !textField(row.KubeconfigSecret, true, 253) {
		return gateways.ErrInvalid
	}
	if row.Status != nil && !textField(*row.Status, false, 255) {
		return gateways.ErrInvalid
	}
	if row.ApiServerUrl != nil && !textField(*row.ApiServerUrl, false, 2048) {
		return gateways.ErrInvalid
	}
	return nil
}

type ReleaseCreate struct {
	Name            string  `json:"name,omitempty"`
	Image           string  `json:"image,omitempty"`
	RolloutStrategy *string `json:"rollout_strategy,omitempty"`
	CanaryPercent   *int32  `json:"canary_percent,omitempty"`
	CanaryDuration  *string `json:"canary_duration,omitempty"`
	Status          *string `json:"status,omitempty"`
}

type ReleasePatch struct {
	Name            *string `json:"name,omitempty"`
	Image           *string `json:"image,omitempty"`
	RolloutStrategy *string `json:"rollout_strategy,omitempty"`
	CanaryPercent   *int32  `json:"canary_percent,omitempty"`
	CanaryDuration  *string `json:"canary_duration,omitempty"`
	Status          *string `json:"status,omitempty"`
}

func newRelease(id string, input ReleaseCreate) (model.GatewayRelease, error) {
	row := model.GatewayRelease{Meta: model.Meta{ID: id}, Name: input.Name, Image: input.Image, RolloutStrategy: input.RolloutStrategy, CanaryPercent: input.CanaryPercent, CanaryDuration: input.CanaryDuration, Status: input.Status}
	return row, validateRelease(row)
}
func patchRelease(row *model.GatewayRelease, input ReleasePatch) error {
	if input.Name != nil {
		row.Name = *input.Name
	}
	if input.Image != nil {
		row.Image = *input.Image
	}
	if input.RolloutStrategy != nil {
		row.RolloutStrategy = input.RolloutStrategy
	}
	if input.CanaryPercent != nil {
		row.CanaryPercent = input.CanaryPercent
	}
	if input.CanaryDuration != nil {
		row.CanaryDuration = input.CanaryDuration
	}
	if input.Status != nil {
		row.Status = input.Status
	}
	return validateRelease(*row)
}
func validateRelease(row model.GatewayRelease) error {
	if !textField(row.Name, true, 255) {
		return gateways.ErrInvalid
	}
	if !textField(row.Image, true, 2048) {
		return gateways.ErrInvalid
	}
	if row.RolloutStrategy != nil && !textField(*row.RolloutStrategy, false, 64) {
		return gateways.ErrInvalid
	}
	if row.CanaryPercent != nil && (*row.CanaryPercent < 0 || *row.CanaryPercent > 100) {
		return gateways.ErrInvalid
	}
	if row.CanaryDuration != nil && !textField(*row.CanaryDuration, false, 64) {
		return gateways.ErrInvalid
	}
	if row.Status != nil && !textField(*row.Status, false, 255) {
		return gateways.ErrInvalid
	}
	return nil
}

type DatabaseCreate struct {
	Name             string  `json:"name,omitempty"`
	Provider         string  `json:"provider,omitempty"`
	Region           *string `json:"region,omitempty"`
	Engine           *string `json:"engine,omitempty"`
	EngineVersion    *string `json:"engine_version,omitempty"`
	InstanceClass    *string `json:"instance_class,omitempty"`
	ConnectionSecret *string `json:"connection_secret,omitempty"`
	Status           *string `json:"status,omitempty"`
}

type DatabasePatch struct {
	Name             *string `json:"name,omitempty"`
	Provider         *string `json:"provider,omitempty"`
	Region           *string `json:"region,omitempty"`
	Engine           *string `json:"engine,omitempty"`
	EngineVersion    *string `json:"engine_version,omitempty"`
	InstanceClass    *string `json:"instance_class,omitempty"`
	ConnectionSecret *string `json:"connection_secret,omitempty"`
	Status           *string `json:"status,omitempty"`
}

func newDatabase(id string, input DatabaseCreate) (model.ManagedDatabase, error) {
	row := model.ManagedDatabase{Meta: model.Meta{ID: id}, Name: input.Name, Provider: input.Provider, Region: input.Region, Engine: input.Engine, EngineVersion: input.EngineVersion, InstanceClass: input.InstanceClass, ConnectionSecret: input.ConnectionSecret, Status: input.Status}
	var err error
	row.Namespace, err = DatabaseNamespace(id)
	if err != nil {
		return row, err
	}
	return row, validateDatabase(row)
}
func patchDatabase(row *model.ManagedDatabase, input DatabasePatch) error {
	if input.Provider != nil && *input.Provider != row.Provider {
		return gateways.ErrInvalid
	}
	if input.Name != nil {
		row.Name = *input.Name
	}
	if input.Provider != nil {
		row.Provider = *input.Provider
	}
	if input.Region != nil {
		row.Region = input.Region
	}
	if input.Engine != nil {
		row.Engine = input.Engine
	}
	if input.EngineVersion != nil {
		row.EngineVersion = input.EngineVersion
	}
	if input.InstanceClass != nil {
		row.InstanceClass = input.InstanceClass
	}
	if input.ConnectionSecret != nil {
		row.ConnectionSecret = input.ConnectionSecret
	}
	if input.Status != nil {
		row.Status = input.Status
	}
	return validateDatabase(*row)
}
func validateDatabase(row model.ManagedDatabase) error {
	if !textField(row.Name, true, 261) {
		return gateways.ErrInvalid
	}
	if row.Provider != "cnpg" && row.Provider != "deployment" {
		return gateways.ErrInvalid
	}
	if !textField(row.Namespace, true, 29) {
		return gateways.ErrInvalid
	}
	if row.Region != nil && !textField(*row.Region, false, 255) {
		return gateways.ErrInvalid
	}
	if row.Engine != nil && !textField(*row.Engine, false, 64) {
		return gateways.ErrInvalid
	}
	if row.EngineVersion != nil && !textField(*row.EngineVersion, false, 64) {
		return gateways.ErrInvalid
	}
	if row.InstanceClass != nil && !textField(*row.InstanceClass, false, 255) {
		return gateways.ErrInvalid
	}
	if row.ConnectionSecret != nil && !textField(*row.ConnectionSecret, false, 253) {
		return gateways.ErrInvalid
	}
	if row.Status != nil && !textField(*row.Status, false, 255) {
		return gateways.ErrInvalid
	}
	return nil
}

// Deleted returns a bounded page of tombstones in database ID order. The cursor
// is a canonical ID. A live watch must start before the first page is requested.
func (r *Resource[T, C, P]) Deleted(ctx context.Context, p gateways.Principal, after string, limit int) ([]T, bool, error) {
	if err := r.authorizeRecovery(p); err != nil {
		return nil, false, err
	}
	if after != "" && !validID(after) {
		return nil, false, gateways.ErrInvalid
	}
	options := store.CursorOptions{AfterID: after, Limit: limit, Deletion: store.CursorDeleted}
	var rows []T
	var more bool
	err := r.repository.WithTransaction(ctx, func(ctx context.Context, tx store.Transaction) error {
		reader, ok := tx.(store.CursorReader)
		if !ok {
			return errors.New("catalog storage does not support cursor reads")
		}
		result, err := reader.ReadCursor(ctx, r.entity, "", "", options)
		if err != nil {
			return err
		}
		rows, ok = result.Items.([]T)
		if !ok {
			return errors.New("unexpected catalog storage result")
		}
		more = result.More
		return nil
	})
	return rows, more, err
}
