package httpapi

import (
	"context"
	"io"
	"net/http"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/users"
	contract "github.com/jsell-rh/hypershell-stego/out/application/contract"
	"github.com/jsell-rh/hypershell-stego/out/application/responses"
	"github.com/jsell-rh/hypershell-stego/out/application/transport"
	auth "github.com/jsell-rh/hypershell-stego/out/auth"
)

const currentUserPath = "/api/hypershell/v1/users/me"

func registerCurrentUser(mux *http.ServeMux, verifier *requestAuth, service *users.Service) error {
	handler, err := endpoint(verifier, func(r *http.Request) (struct{}, error) {
		if r.URL.RawQuery != "" {
			return struct{}{}, transport.ErrRequest
		}
		if r.Body != nil {
			data, err := io.ReadAll(io.LimitReader(r.Body, 1))
			if err != nil || len(data) > 0 {
				return struct{}{}, transport.ErrRequest
			}
		}
		return struct{}{}, nil
	}, func(ctx context.Context, _ struct{}) (*contract.CurrentUser, error) {
		user, err := service.Current(ctx, gateways.PrincipalFromContext(ctx))
		if err != nil {
			return nil, err
		}
		identity := auth.IdentityFromContext(ctx)
		return responses.CurrentUser(user, responses.CurrentUserInput{
			Issuer:    identity.Issuer,
			Subject:   identity.UserID,
			ExpiresAt: identity.ExpiresAt.UTC(),
		})
	}, http.StatusOK, writeError)
	if err != nil {
		return err
	}
	mux.Handle("GET "+currentUserPath, handler)
	return nil
}
