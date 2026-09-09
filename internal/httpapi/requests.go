package httpapi

import (
	"context"
	"net/http"

	"github.com/jsell-rh/hypershell-stego/out/application/transport"
)

type requestAuth struct {
	Authenticate transport.Authenticate
	Prepare      transport.Prepare
}

func endpoint[Request, Response any](auth *requestAuth, decode func(*http.Request) (Request, error), call func(context.Context, Request) (Response, error), status int, writeError transport.ErrorHandler) (http.Handler, error) {
	return transport.Endpoint(auth.Authenticate, decode, call, status, writeError, auth.Prepare)
}
func replyEndpoint[Request, Response any](auth *requestAuth, decode func(*http.Request) (Request, error), call func(context.Context, Request) (transport.Reply[Response], error), writeError transport.ErrorHandler) (http.Handler, error) {
	return transport.ReplyEndpoint(auth.Authenticate, decode, call, writeError, auth.Prepare)
}
