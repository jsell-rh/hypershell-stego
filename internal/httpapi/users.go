package httpapi

import (
	"context"
	"io"
	"net/http"

	"github.com/jsell-rh/hypershell-stego/internal/gateways"
	"github.com/jsell-rh/hypershell-stego/internal/users"
	"github.com/jsell-rh/hypershell-stego/out/application/transport"
)

const currentUserPath = "/api/hypershell/v1/users/me"

type CurrentUser struct {
	Reference
	Username string `json:"username"`
	Email    string `json:"email"`
	Name     string `json:"name"`
}

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
	}, func(ctx context.Context, _ struct{}) (CurrentUser, error) {
		user, err := service.Current(ctx, gateways.PrincipalFromContext(ctx))
		if err != nil {
			return CurrentUser{}, err
		}
		return CurrentUser{Reference: Reference{ID: user.ID, Kind: "User", Href: currentUserPath, CreatedAt: user.CreatedTime, UpdatedAt: user.UpdatedTime}, Username: user.Username, Email: user.Email, Name: user.Name}, nil
	}, http.StatusOK, writeError)
	if err != nil {
		return err
	}
	mux.Handle("GET "+currentUserPath, handler)
	return nil
}
