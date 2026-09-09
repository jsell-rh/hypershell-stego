package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccounts"
	"github.com/jsell-rh/hypershell-stego/out/application/transport"
	storage "github.com/jsell-rh/hypershell-stego/out/contracts/storage"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

type accountItem struct {
	ID              string     `json:"id"`
	GatewayID       string     `json:"gateway_id"`
	Name            string     `json:"name"`
	Description     *string    `json:"description"`
	CredentialType  string     `json:"credential_type"`
	Role            string     `json:"role"`
	Status          string     `json:"status"`
	CreatedByUserID string     `json:"created_by_user_id"`
	ClientID        string     `json:"client_id"`
	Subject         string     `json:"subject"`
	ExpiresAt       time.Time  `json:"expires_at"`
	RevokedAt       *time.Time `json:"revoked_at"`
	LastError       *string    `json:"last_error"`
	CreatedAt       time.Time  `json:"created_at"`
	UpdatedAt       time.Time  `json:"updated_at"`
}
type accountCredential struct {
	serviceaccounts.Connection
	ClientSecret string `json:"client_secret"`
}
type accountCreateResponse struct {
	accountItem
	Credential accountCredential `json:"credential"`
}
type accountGetResponse struct {
	accountItem
	Connection serviceaccounts.Connection `json:"connection"`
}
type expirationPolicy struct {
	DefaultSeconds int64 `json:"default_seconds"`
	MinimumSeconds int64 `json:"minimum_seconds"`
	MaximumSeconds int64 `json:"maximum_seconds"`
}
type accountCapabilities struct {
	CanCreate        bool             `json:"can_create"`
	AllowedRoles     []string         `json:"allowed_roles"`
	CanManageAll     bool             `json:"can_manage_all"`
	ExpirationPolicy expirationPolicy `json:"expiration_policy"`
}
type accountListResponse struct {
	Page         int                 `json:"page"`
	Size         int                 `json:"size"`
	Total        int64               `json:"total"`
	Capabilities accountCapabilities `json:"capabilities"`
	Items        []accountItem       `json:"items"`
}
type accountInput struct {
	GatewayID, ID string
	Create        serviceaccounts.CreateRequest
	Page, Size    int
}

func presentAccount(row model.ServiceAccount) accountItem {
	return accountItem{ID: row.ID, GatewayID: row.GatewayID, Name: row.Name, Description: row.Description, CredentialType: row.CredentialType, Role: row.Role, Status: row.Status, CreatedByUserID: row.CreatedByUserID, ClientID: row.ClientID, Subject: row.Subject, ExpiresAt: row.ExpiresAt, RevokedAt: row.RevokedAt, LastError: row.LastError, CreatedAt: row.CreatedTime, UpdatedAt: row.UpdatedTime}
}
func accountTarget(r *http.Request) (accountInput, error) {
	if r.URL.RawQuery != "" {
		return accountInput{}, transport.ErrRequest
	}
	return accountInput{GatewayID: r.PathValue("gateway_id"), ID: r.PathValue("account_id")}, nil
}
func registerAccounts(mux *http.ServeMux, verifier *requestAuth, service *serviceaccounts.Service) error {
	path := collectionPath + "/{gateway_id}/service_accounts"
	create, err := endpoint(verifier, func(r *http.Request) (accountInput, error) {
		target, err := accountTarget(r)
		if err != nil {
			return target, err
		}
		target.Create, err = transport.JSONBody[serviceaccounts.CreateRequest](r)
		return target, err
	}, func(ctx context.Context, input accountInput) (accountCreateResponse, error) {
		created, err := service.Create(ctx, gateways.PrincipalFromContext(ctx), input.GatewayID, input.Create)
		if err != nil {
			return accountCreateResponse{}, err
		}
		return accountCreateResponse{accountItem: presentAccount(created.Account), Credential: accountCredential{Connection: created.Connection, ClientSecret: created.Secret}}, nil
	}, 201, accountError)
	if err != nil {
		return err
	}
	mux.Handle("POST "+path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Pragma", "no-cache")
		create.ServeHTTP(w, r)
	}))
	get, err := endpoint(verifier, accountTarget, func(ctx context.Context, input accountInput) (accountGetResponse, error) {
		row, connection, err := service.Get(ctx, gateways.PrincipalFromContext(ctx), input.GatewayID, input.ID)
		if err != nil {
			return accountGetResponse{}, err
		}
		return accountGetResponse{accountItem: presentAccount(row), Connection: connection}, nil
	}, 200, accountError)
	if err != nil {
		return err
	}
	mux.Handle("GET "+path+"/{account_id}", get)
	list, err := endpoint(verifier, func(r *http.Request) (accountInput, error) {
		input := accountInput{GatewayID: r.PathValue("gateway_id"), Page: 1, Size: 20}
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			return input, err
		}
		for key, values := range query {
			if len(values) != 1 {
				return input, transport.ErrRequest
			}
			value, err := strconv.Atoi(values[0])
			if err != nil {
				return input, transport.ErrRequest
			}
			switch key {
			case "page":
				input.Page = value
			case "size":
				input.Size = value
			default:
				return input, transport.ErrRequest
			}
		}
		return input, nil
	}, func(ctx context.Context, input accountInput) (accountListResponse, error) {
		result, access, err := service.List(ctx, gateways.PrincipalFromContext(ctx), input.GatewayID, input.Page, input.Size)
		if err != nil {
			return accountListResponse{}, err
		}
		roles := []string{serviceaccounts.RoleUser}
		if access.Owner {
			roles = append(roles, serviceaccounts.RoleAdmin)
		}
		response := accountListResponse{Page: input.Page, Size: input.Size, Total: result.Total, Capabilities: accountCapabilities{CanCreate: true, AllowedRoles: roles, CanManageAll: access.Owner, ExpirationPolicy: expirationPolicy{DefaultSeconds: int64(serviceaccounts.DefaultExpiration / time.Second), MinimumSeconds: int64(serviceaccounts.MinimumExpiration / time.Second), MaximumSeconds: int64(serviceaccounts.MaximumExpiration / time.Second)}}, Items: []accountItem{}}
		rows, ok := result.Items.([]model.ServiceAccount)
		if !ok {
			return accountListResponse{}, errors.New("unexpected account list")
		}
		for _, row := range rows {
			response.Items = append(response.Items, presentAccount(row))
		}
		return response, nil
	}, 200, accountError)
	if err != nil {
		return err
	}
	mux.Handle("GET "+path, list)
	for _, remove := range []bool{false, true} {
		handler, err := replyEndpoint(verifier, accountTarget, func(ctx context.Context, input accountInput) (transport.Reply[accountItem], error) {
			row, complete, err := service.Change(ctx, gateways.PrincipalFromContext(ctx), input.GatewayID, input.ID, remove)
			if err != nil {
				return transport.Reply[accountItem]{}, err
			}
			if remove && complete {
				return transport.Reply[accountItem]{Status: 204}, nil
			}
			status := 202
			if complete {
				status = 200
			}
			item := presentAccount(row)
			return transport.Reply[accountItem]{Status: status, Value: &item}, nil
		}, accountError)
		if err != nil {
			return err
		}
		if remove {
			mux.Handle("DELETE "+path+"/{account_id}", handler)
		} else {
			mux.Handle("POST "+path+"/{account_id}/revoke", handler)
		}
	}
	return nil
}
func accountError(w http.ResponseWriter, r *http.Request, err error) {
	code, message := 500, "The request could not be completed"
	name := "internal_error"
	switch {
	case errors.Is(err, storage.ErrNotFound):
		code, name, message = 404, "not_found", "The gateway service account was not found"
	case errors.Is(err, serviceaccounts.ErrRole):
		code, name, message = 403, "role_not_allowed", "The requested role is not permitted"
	case errors.Is(err, serviceaccounts.ErrGatewayQuota):
		code, name, message = 429, "gateway_quota_exceeded", "The Gateway service-account quota is exhausted"
	case errors.Is(err, serviceaccounts.ErrQuota):
		code, name, message = 429, "creator_quota_exceeded", "The service-account quota is exhausted"
	case errors.Is(err, serviceaccounts.ErrNotReady):
		code, name, message = 409, "gateway_not_ready", "The gateway is not ready"
	case errors.Is(err, serviceaccounts.ErrBusy):
		code, name, message = 409, "operation_pending", "The service-account operation is pending"
	case errors.Is(err, storage.ErrConflict):
		code, name, message = 409, "service_account_name_exists", "An active service account already uses this name"
	case errors.Is(err, serviceaccounts.ErrUnavailable):
		code, name, message = 503, "keycloak_unavailable", "Service-account provisioning is unavailable"
	case errors.Is(err, transport.ErrUnauthenticated), errors.Is(err, gateways.ErrIdentity):
		code, name, message = 401, "unauthorized", "Authentication is required"
	case errors.Is(err, transport.ErrRequest), errors.Is(err, gateways.ErrInvalid):
		code, name, message = 400, "invalid_request", "The request is invalid"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded), errors.Is(err, storage.ErrSerialization):
		code, name, message = 503, "service_unavailable", "The request could not complete"
	}
	writeAccountProblem(w, code, name, message)
}

func writeAccountProblem(w http.ResponseWriter, code int, name, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"code": name, "reason": message})
}

type managedApplication struct {
	http.Handler
	accounts *serviceaccounts.Service
	close    func()
}

func (a *managedApplication) Run(ctx context.Context) error { return a.accounts.Run(ctx) }
func (a *managedApplication) Close()                        { a.close() }
