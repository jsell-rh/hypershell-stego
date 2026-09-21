package httpapi

import (
	"context"
	"io"
	"net/http"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/out/application/contract"
	"github.com/jsell-rh/hypershell-stego/out/application/responses"
	"github.com/jsell-rh/hypershell-stego/out/application/transport"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

const grantPath = "/api/hypershell/v1/role_bindings"

type grantItem = contract.RoleBinding

type grantList struct {
	Kind  string       `json:"kind"`
	Href  string       `json:"href"`
	Page  int          `json:"page"`
	Size  int          `json:"size"`
	Total int64        `json:"total"`
	Items []*grantItem `json:"items"`
}

func presentGrant(row model.RoleBinding) (*grantItem, error) {
	return responses.RoleBinding(row)
}

func registerGrants(mux *http.ServeMux, verifier *requestAuth, service *gateways.Service) error {
	list, err := endpoint(verifier, func(r *http.Request) (pageRequest, error) {
		return parseEntityPage(r, "RoleBinding")
	}, func(ctx context.Context, q pageRequest) (any, error) {
		page, err := service.ListGrants(ctx, gateways.PrincipalFromContext(ctx), gateways.GrantQuery{Page: q.Page, Size: q.Size, Search: q.Search, OrderBy: q.OrderBy})
		if err != nil {
			return nil, err
		}
		result := grantList{Kind: "RoleBindingList", Href: grantPath, Page: q.Page, Size: len(page.Items), Total: page.Total, Items: make([]*grantItem, 0, len(page.Items))}
		for _, item := range page.Items {
			value, err := presentGrant(item.Grant)
			if err != nil {
				return nil, err
			}
			result.Items = append(result.Items, value)
		}
		return transport.ProjectListIfSelected(result, q.Fields, "items")
	}, http.StatusOK, writeError)
	if err != nil {
		return err
	}
	create, err := endpoint(verifier, func(r *http.Request) (gateways.GrantRequest, error) {
		if r.URL.RawQuery != "" {
			return gateways.GrantRequest{}, transport.ErrRequest
		}
		return transport.JSONBody[gateways.GrantRequest](r)
	}, func(ctx context.Context, input gateways.GrantRequest) (*grantItem, error) {
		row, err := service.CreateGrant(ctx, gateways.PrincipalFromContext(ctx), input)
		if err != nil {
			return nil, err
		}
		return presentGrant(row)
	}, http.StatusCreated, writeError)
	if err != nil {
		return err
	}
	target := func(r *http.Request) (string, error) {
		if r.URL.RawQuery != "" {
			return "", transport.ErrRequest
		}
		if r.Body != nil {
			body, err := io.ReadAll(io.LimitReader(r.Body, 1))
			if err != nil || len(body) > 0 {
				return "", transport.ErrRequest
			}
		}
		return r.PathValue("id"), nil
	}
	get, err := endpoint(verifier, target, func(ctx context.Context, id string) (*grantItem, error) {
		row, err := service.GetGrant(ctx, gateways.PrincipalFromContext(ctx), id)
		if err != nil {
			return nil, err
		}
		return presentGrant(row)
	}, http.StatusOK, writeError)
	if err != nil {
		return err
	}
	remove, err := endpoint(verifier, target, func(ctx context.Context, id string) (transport.NoContent, error) {
		return transport.NoContent{}, service.DeleteGrant(ctx, gateways.PrincipalFromContext(ctx), id)
	}, http.StatusNoContent, writeError)
	if err != nil {
		return err
	}
	mux.Handle("POST "+grantPath, create)
	mux.Handle("GET "+grantPath, list)
	mux.Handle("GET "+grantPath+"/{id}", get)
	mux.Handle("DELETE "+grantPath+"/{id}", remove)
	return nil
}
