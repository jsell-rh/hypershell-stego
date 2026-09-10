package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/jsell-rh/hypershell-stego/internal/roles"
	"github.com/jsell-rh/hypershell-stego/out/application/transport"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

const rolePath = "/api/hypershell/v1/roles"

type Role struct {
	Reference
	Name        string          `json:"name"`
	DisplayName *string         `json:"display_name,omitempty"`
	Description *string         `json:"description,omitempty"`
	Permissions json.RawMessage `json:"permissions,omitempty"`
	BuiltIn     bool            `json:"built_in"`
}
type RoleList struct {
	Kind  string `json:"kind"`
	Href  string `json:"href"`
	Page  int    `json:"page"`
	Size  int    `json:"size"`
	Total int64  `json:"total"`
	Items []Role `json:"items"`
}

func presentRole(row model.Role) (Role, error) {
	if len(row.Permissions) > 0 {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(row.Permissions, &object); err != nil || object == nil {
			return Role{}, errors.New("stored role permissions must be an object")
		}
	}
	return Role{Reference: Reference{ID: row.ID, Kind: "Role", Href: rolePath + "/" + row.ID, CreatedAt: row.CreatedTime, UpdatedAt: row.UpdatedTime}, Name: row.Name, DisplayName: row.DisplayName, Description: row.Description, Permissions: json.RawMessage(row.Permissions), BuiltIn: row.BuiltIn}, nil
}
func registerRoles(mux *http.ServeMux, verifier *requestAuth, service *roles.Service) error {
	list, err := endpoint(verifier, func(r *http.Request) (pageRequest, error) { return parseEntityPage(r, "Role") }, func(ctx context.Context, q pageRequest) (any, error) {
		result, err := service.List(ctx, roles.Query{Page: q.Page, Size: q.Size, Search: q.Search, OrderBy: q.OrderBy})
		if err != nil {
			return nil, err
		}
		rows, ok := result.Items.([]model.Role)
		if !ok {
			return nil, errors.New("unexpected role storage result")
		}
		response := RoleList{Kind: "RoleList", Href: rolePath, Page: q.Page, Size: len(rows), Total: result.Total, Items: make([]Role, 0, len(rows))}
		for _, row := range rows {
			item, err := presentRole(row)
			if err != nil {
				return nil, err
			}
			response.Items = append(response.Items, item)
		}
		return transport.ProjectListIfSelected(response, q.Fields, "items")
	}, http.StatusOK, writeError)
	if err != nil {
		return err
	}
	get, err := endpoint(verifier, func(r *http.Request) (string, error) {
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
	}, func(ctx context.Context, id string) (Role, error) {
		row, err := service.Get(ctx, id)
		if err != nil {
			return Role{}, err
		}
		return presentRole(row)
	}, http.StatusOK, writeError)
	if err != nil {
		return err
	}
	mux.Handle("GET "+rolePath, list)
	mux.Handle("GET "+rolePath+"/{id}", get)
	return nil
}
