// Package httpapi maps the Hypershell REST contract to domain operations.
package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"

	placement "github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/roles"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	"github.com/jsell-rh/hypershell-stego/internal/users"
	"github.com/jsell-rh/hypershell-stego/out/application/transport"
	"github.com/jsell-rh/hypershell-stego/out/auth"
	contract "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	search "github.com/jsell-rh/hypershell-stego/out/search"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

const collectionPath = "/api/hypershell/v1/gateways"

type Reference struct {
	ID        string    `json:"id,omitempty"`
	Kind      string    `json:"kind"`
	Href      string    `json:"href"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type Gateway struct {
	Reference
	Name               string   `json:"name"`
	ClusterID          string   `json:"cluster_id"`
	ReleaseID          string   `json:"release_id"`
	DatabaseID         string   `json:"database_id"`
	Namespace          string   `json:"namespace"`
	ExternalDNS        *string  `json:"external_dns,omitempty"`
	TLSMode            *string  `json:"tls_mode,omitempty"`
	ServiceType        *string  `json:"service_type,omitempty"`
	Status             *string  `json:"status,omitempty"`
	Phase              *string  `json:"phase,omitempty"`
	Image              *string  `json:"image,omitempty"`
	SupervisorImage    *string  `json:"supervisor_image,omitempty"`
	ServerDNSNames     []string `json:"server_dns_names,omitempty"`
	RouteAddress       *string  `json:"route_address,omitempty"`
	ConsoleAddress     *string  `json:"console_address,omitempty"`
	OIDC               *string  `json:"oidc,omitempty"`
	Route              *string  `json:"route,omitempty"`
	CredentialDriver   *string  `json:"credential_driver,omitempty"`
	ActiveSandboxCount *int32   `json:"active_sandbox_count,omitempty"`
	CreatedBy          string   `json:"created_by,omitempty"`
}
type GatewayList struct {
	Kind  string    `json:"kind"`
	Href  string    `json:"href"`
	Page  int       `json:"page"`
	Size  int       `json:"size"`
	Total int64     `json:"total"`
	Items []Gateway `json:"items"`
}
type patchRequest struct {
	ID    string
	Patch gateways.PatchRequest
}

type pageRequest struct {
	Page, Size int
	Search     string
	OrderBy    []contract.OrderByField
}

func New(repository gateways.Repository, rawVerifier *auth.Verifier, database *sql.DB) (http.Handler, error) {
	options, err := gateways.OptionsFromEnvironment()
	if err != nil {
		return nil, err
	}
	provider, closeProvider, err := serviceaccounts.ProvisionerFromEnvironment()
	if err != nil {
		return nil, err
	}
	complete := false
	defer func() {
		if !complete {
			closeProvider()
		}
	}()
	accounts, err := serviceaccounts.New(repository, provider)
	if err != nil {
		return nil, err
	}
	options.AccountCleaner = accounts
	service, err := gateways.New(repository, options)
	if err != nil {
		return nil, err
	}
	if rawVerifier == nil || database == nil {
		return nil, errors.New("HTTP application requires a verifier")
	}
	verifier := &requestAuth{Authenticate: rawVerifier.Authenticate, Prepare: func(ctx context.Context) error {
		return service.PrepareRequest(ctx, gateways.PrincipalFromContext(ctx))
	}}
	mux := http.NewServeMux()
	placementService, err := placement.New(repository, service)
	if err != nil {
		return nil, err
	}
	if err := registerPlacement(mux, verifier, placementService); err != nil {
		return nil, err
	}
	create, err := endpoint(verifier, func(r *http.Request) (gateways.CreateRequest, error) {
		return transport.JSONBody[gateways.CreateRequest](r)
	}, func(ctx context.Context, request gateways.CreateRequest) (Gateway, error) {
		row, err := service.Create(ctx, gateways.PrincipalFromContext(ctx), request)
		if err != nil {
			return Gateway{}, err
		}
		return present(row, gateways.PrincipalFromContext(ctx).Username)
	}, http.StatusCreated, writeError)
	if err != nil {
		return nil, err
	}
	get, err := endpoint(verifier, func(r *http.Request) (string, error) {
		if r.URL.RawQuery != "" {
			return "", transport.ErrRequest
		}
		return r.PathValue("id"), nil
	}, func(ctx context.Context, id string) (Gateway, error) {
		row, err := service.Get(ctx, gateways.PrincipalFromContext(ctx), id)
		if err != nil {
			return Gateway{}, err
		}
		creators, err := creatorNames(ctx, database, []string{row.ID})
		if err != nil {
			return Gateway{}, err
		}
		return present(row, creators[row.ID])
	}, http.StatusOK, writeError)
	if err != nil {
		return nil, err
	}
	list, err := endpoint(verifier, parsePage, func(ctx context.Context, request pageRequest) (GatewayList, error) {
		result, err := service.Search(ctx, gateways.PrincipalFromContext(ctx), request.Page, request.Size, request.Search, request.OrderBy)
		if err != nil {
			return GatewayList{}, err
		}
		rows, ok := result.Items.([]model.Gateway)
		if !ok {
			return GatewayList{}, errors.New("unexpected Gateway storage result")
		}
		response := GatewayList{Kind: "GatewayList", Href: collectionPath, Page: request.Page, Size: len(rows), Total: result.Total, Items: make([]Gateway, 0, len(rows))}
		ids := make([]string, len(rows))
		for i, row := range rows {
			ids[i] = row.ID
		}
		creators, err := creatorNames(ctx, database, ids)
		if err != nil {
			return GatewayList{}, err
		}
		for _, row := range rows {
			item, err := present(row, creators[row.ID])
			if err != nil {
				return GatewayList{}, err
			}
			response.Items = append(response.Items, item)
		}
		return response, nil
	}, http.StatusOK, writeError)
	if err != nil {
		return nil, err
	}
	patch, err := endpoint(verifier, func(r *http.Request) (patchRequest, error) {
		if r.URL.RawQuery != "" {
			return patchRequest{}, transport.ErrRequest
		}
		patch, err := transport.JSONBody[gateways.PatchRequest](r)
		return patchRequest{ID: r.PathValue("id"), Patch: patch}, err
	}, func(ctx context.Context, request patchRequest) (Gateway, error) {
		row, err := service.Update(ctx, gateways.PrincipalFromContext(ctx), request.ID, request.Patch)
		if err != nil {
			return Gateway{}, err
		}
		creators, err := creatorNames(ctx, database, []string{row.ID})
		if err != nil {
			return Gateway{}, err
		}
		return present(row, creators[row.ID])
	}, http.StatusOK, writeError)
	if err != nil {
		return nil, err
	}
	remove, err := endpoint(verifier, func(r *http.Request) (string, error) {
		if r.URL.RawQuery != "" {
			return "", transport.ErrRequest
		}
		if r.Body != nil {
			data, err := io.ReadAll(io.LimitReader(r.Body, 1))
			if err != nil || len(data) > 0 {
				return "", transport.ErrRequest
			}
		}
		return r.PathValue("id"), nil
	}, func(ctx context.Context, id string) (transport.NoContent, error) {
		return transport.NoContent{}, service.Delete(ctx, gateways.PrincipalFromContext(ctx), id)
	}, http.StatusNoContent, writeError)
	if err != nil {
		return nil, err
	}
	mux.Handle("PATCH "+collectionPath+"/{id}", patch)
	mux.Handle("DELETE "+collectionPath+"/{id}", remove)
	mux.Handle("POST "+collectionPath, create)
	mux.Handle("GET "+collectionPath+"/{id}", get)
	mux.Handle("GET "+collectionPath, list)
	userService, err := users.New(repository)
	if err != nil {
		return nil, err
	}
	if err := registerCurrentUser(mux, verifier, userService); err != nil {
		return nil, err
	}
	catalog, err := roles.New(repository)
	if err != nil {
		return nil, err
	}
	if err := registerRoles(mux, verifier, catalog); err != nil {
		return nil, err
	}
	if err := registerAccounts(mux, verifier, accounts); err != nil {
		return nil, err
	}
	if err := registerGrants(mux, verifier, service); err != nil {
		return nil, err
	}
	complete = true
	return &managedApplication{Handler: mux, accounts: accounts, close: closeProvider}, nil
}

func present(row model.Gateway, creator string) (Gateway, error) {
	row = row.CurrentObservations()
	var names []string
	if len(row.ServerDnsNames) > 0 {
		if err := json.Unmarshal(row.ServerDnsNames, &names); err != nil {
			return Gateway{}, errors.New("stored server DNS names are invalid")
		}
	}
	return Gateway{Reference: Reference{ID: row.ID, Kind: "Gateway", Href: collectionPath + "/" + row.ID, CreatedAt: row.CreatedTime, UpdatedAt: row.UpdatedTime}, Name: row.Name, ClusterID: row.ClusterID, ReleaseID: row.ReleaseID, DatabaseID: row.DatabaseID, Namespace: row.Namespace,
		ExternalDNS: row.ExternalDns, TLSMode: row.TlsMode, ServiceType: row.ServiceType, Status: row.Status, Phase: row.Phase, Image: row.Image, SupervisorImage: row.SupervisorImage, ServerDNSNames: names, RouteAddress: row.RouteAddress, ConsoleAddress: row.ConsoleAddress, OIDC: row.Oidc, Route: row.Route, CredentialDriver: row.CredentialDriver, ActiveSandboxCount: row.ActiveSandboxCount, CreatedBy: creator}, nil
}

func parsePage(r *http.Request) (pageRequest, error) {
	return parseEntityPage(r, "Gateway")
}

func parseEntityPage(r *http.Request, entity string) (pageRequest, error) {
	values, err := urlValues(r)
	if err != nil {
		return pageRequest{}, err
	}
	request := pageRequest{Page: 1, Size: 100}
	for name, value := range values {
		if len(value) != 1 {
			return pageRequest{}, transport.ErrRequest
		}
		switch name {
		case "search":
			request.Search = value[0]
		case "orderBy":
			request.OrderBy, err = transport.ParseOrderBy(value[0], search.EntityFieldMaps[entity])
			if err != nil {
				return pageRequest{}, err
			}
		case "page", "size":
			n, err := strconv.Atoi(value[0])
			if err != nil {
				return pageRequest{}, transport.ErrRequest
			}
			if name == "page" {
				request.Page = n
			} else {
				request.Size = n
			}
		default:
			return pageRequest{}, transport.ErrRequest
		}
	}
	if request.Size > 100 {
		return pageRequest{}, gateways.ErrInvalid
	}
	return request, nil
}
func urlValues(r *http.Request) (map[string][]string, error) {
	if len(r.URL.RawQuery) > 4096 {
		return nil, transport.ErrRequest
	}
	values, err := url.ParseQuery(r.URL.RawQuery)
	if err != nil {
		return nil, transport.ErrRequest
	}
	return values, nil
}

// creatorNames implements the reference rule: the earliest live owner with a
// live user supplies created_by. One query serves the complete visible page.
func creatorNames(ctx context.Context, database *sql.DB, ids []string) (map[string]string, error) {
	result := make(map[string]string, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	if len(ids) > 100 {
		return nil, errors.New("creator lookup exceeds its page limit")
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	rows, err := database.QueryContext(ctx, `SELECT DISTINCT ON (b.gateway_id) b.gateway_id,u.username
 FROM role_bindings b
 JOIN users u ON u.id=b.user_id AND u.deleted_at IS NULL
 JOIN roles r ON r.id=b.role_id AND r.deleted_at IS NULL
 WHERE b.deleted_at IS NULL AND b.scope='gateway' AND r.name='gateway:owner'
 AND b.gateway_id=ANY($1::text[])
 ORDER BY b.gateway_id,b.created_time ASC,b.id ASC`, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id, name string
		if err := rows.Scan(&id, &name); err != nil {
			return nil, err
		}
		result[id] = name
	}
	return result, rows.Err()
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	code, reason := http.StatusInternalServerError, "An internal error occurred"
	errorID := 9
	switch {
	case errors.Is(err, gateways.ErrObservationOwned):
		code, reason, errorID = http.StatusForbidden, "Phase and status are controller-owned fields", 4
	case errors.Is(err, gateways.ErrObservationRequired):
		code, reason = http.StatusPreconditionRequired, "Controller write requires an observed resource version"
	case errors.Is(err, gateways.ErrLastOwner):
		code, reason, errorID = http.StatusConflict, "The last Gateway owner cannot be removed", 6
	case errors.Is(err, gateways.ErrGatewayCleanupUnavailable):
		code, reason = http.StatusServiceUnavailable, "Gateway service-account cleanup is unavailable"
	case errors.Is(err, gateways.ErrServiceAccountsExist):
		code, reason, errorID = http.StatusConflict, "service accounts require cleanup before Gateway deletion", 6
	case errors.Is(err, transport.ErrUnauthenticated), errors.Is(err, gateways.ErrIdentity):
		code, reason = http.StatusUnauthorized, "Authentication is required"
		errorID = 15
	case errors.Is(err, transport.ErrRequest):
		code, reason, errorID = http.StatusBadRequest, "The request is invalid", 17
	case errors.Is(err, gateways.ErrInvalid), errors.Is(err, contract.ErrSearch):
		code, reason = http.StatusBadRequest, "The request is invalid"
		errorID = 8
	case errors.Is(err, gateways.ErrForbidden):
		code, reason = http.StatusForbidden, "The request is forbidden"
		errorID = 4
	case errors.Is(err, contract.ErrNotFound):
		code, reason = http.StatusNotFound, "The resource was not found"
		errorID = 7
	case errors.Is(err, contract.ErrSerialization):
		code, reason = http.StatusConflict, "The resource changed during the request; retry the operation"
		errorID = 6
	case errors.Is(err, contract.ErrConflict):
		code, reason = http.StatusConflict, "The resource conflicts with an existing record"
		errorID = 6
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		code, reason = http.StatusServiceUnavailable, "The request could not complete"
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(struct {
		ID          string `json:"id"`
		Kind        string `json:"kind"`
		Href        string `json:"href"`
		Code        string `json:"code"`
		Reason      string `json:"reason"`
		OperationID string `json:"operation_id"`
	}{strconv.Itoa(errorID), "Error", "/api/hypershell/v1/errors/" + strconv.Itoa(errorID), "hypershell-" + strconv.Itoa(errorID), reason, ""})
}
