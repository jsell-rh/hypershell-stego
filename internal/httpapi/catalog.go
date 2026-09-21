package httpapi

import (
	"context"
	"errors"
	"io"
	"net/http"

	"github.com/jsell-rh/hypershell-stego/internal/catalog"
	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/out/application/contract"
	"github.com/jsell-rh/hypershell-stego/out/application/responses"
	"github.com/jsell-rh/hypershell-stego/out/application/transport"
	model "github.com/jsell-rh/hypershell-stego/out/storage"
)

type catalogList[T any] struct {
	Kind  string `json:"kind"`
	Href  string `json:"href"`
	Page  int    `json:"page"`
	Size  int    `json:"size"`
	Total int64  `json:"total"`
	Items []T    `json:"items"`
}
type catalogPatch[P any] struct {
	ID    string
	Value P
}

func catalogID(r *http.Request) (string, error) {
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
}
func registerCatalog[T, C, P, R any](mux *http.ServeMux, auth *requestAuth, resource *catalog.Resource[T, C, P], entity, path string, present func(T) (R, error)) error {
	create, err := endpoint(auth, func(r *http.Request) (C, error) {
		if r.URL.RawQuery != "" {
			var zero C
			return zero, transport.ErrRequest
		}
		return transport.JSONBody[C](r)
	}, func(ctx context.Context, input C) (R, error) {
		row, err := resource.Create(ctx, gateways.PrincipalFromContext(ctx), input)
		if err != nil {
			var zero R
			return zero, err
		}
		return present(row)
	}, http.StatusCreated, writeError)
	if err != nil {
		return err
	}
	get, err := endpoint(auth, catalogID, func(ctx context.Context, id string) (R, error) {
		row, err := resource.Get(ctx, gateways.PrincipalFromContext(ctx), id)
		if err != nil {
			var zero R
			return zero, err
		}
		return present(row)
	}, http.StatusOK, writeError)
	if err != nil {
		return err
	}
	update, err := endpoint(auth, func(r *http.Request) (catalogPatch[P], error) {
		if r.URL.RawQuery != "" {
			return catalogPatch[P]{}, transport.ErrRequest
		}
		value, err := transport.JSONBody[P](r)
		return catalogPatch[P]{r.PathValue("id"), value}, err
	}, func(ctx context.Context, input catalogPatch[P]) (R, error) {
		row, err := resource.Update(ctx, gateways.PrincipalFromContext(ctx), input.ID, input.Value)
		if err != nil {
			var zero R
			return zero, err
		}
		return present(row)
	}, http.StatusOK, writeError)
	if err != nil {
		return err
	}
	remove, err := endpoint(auth, catalogID, func(ctx context.Context, id string) (transport.NoContent, error) {
		return transport.NoContent{}, resource.Delete(ctx, gateways.PrincipalFromContext(ctx), id)
	}, http.StatusNoContent, writeError)
	if err != nil {
		return err
	}
	list, err := endpoint(auth, func(r *http.Request) (pageRequest, error) { return parseEntityPage(r, entity) }, func(ctx context.Context, q pageRequest) (any, error) {
		result, err := resource.List(ctx, gateways.PrincipalFromContext(ctx), catalog.Query{Page: q.Page, Size: q.Size, Search: q.Search, OrderBy: q.OrderBy})
		if err != nil {
			return nil, err
		}
		rows, ok := result.Items.([]T)
		if !ok {
			return nil, errors.New("unexpected catalog storage result")
		}
		response := catalogList[R]{Kind: entity + "List", Href: path, Page: q.Page, Size: len(rows), Total: result.Total, Items: make([]R, 0, len(rows))}
		for _, row := range rows {
			value, err := present(row)
			if err != nil {
				return nil, err
			}
			response.Items = append(response.Items, value)
		}
		return transport.ProjectListIfSelected(response, q.Fields, "items")
	}, http.StatusOK, writeError)
	if err != nil {
		return err
	}
	mux.Handle("POST "+path, create)
	mux.Handle("GET "+path+"/{id}", get)
	mux.Handle("PATCH "+path+"/{id}", update)
	mux.Handle("DELETE "+path+"/{id}", remove)
	mux.Handle("GET "+path, list)
	return nil
}

type ManagedCluster = contract.ManagedCluster

func presentManagedCluster(row model.ManagedCluster) (*ManagedCluster, error) {
	return responses.ManagedCluster(row)
}

type GatewayRelease = contract.GatewayRelease

func presentGatewayRelease(row model.GatewayRelease) (*GatewayRelease, error) {
	return responses.GatewayRelease(row)
}

type GatewayNetwork = contract.GatewayNetwork

func presentGatewayNetwork(row model.GatewayNetwork) (*GatewayNetwork, error) {
	return responses.GatewayNetwork(row)
}
func registerPlacement(mux *http.ServeMux, auth *requestAuth, service *catalog.Service) error {
	if err := registerCatalog(mux, auth, service.Networks, "GatewayNetwork", "/api/hypershell/v1/gateway_networks", presentGatewayNetwork); err != nil {
		return err
	}
	if err := registerCatalog(mux, auth, service.Clusters, "ManagedCluster", "/api/hypershell/v1/managed_clusters", presentManagedCluster); err != nil {
		return err
	}
	if err := registerCatalog(mux, auth, service.Releases, "GatewayRelease", "/api/hypershell/v1/gateway_releases", presentGatewayRelease); err != nil {
		return err
	}
	return nil
}
