package gateways

import (
	"context"
	"strings"

	"github.com/jsell-rh/hypershell-stego/out/auth"
)

// PrincipalFromContext reads claims placed by the generated verifier.
// A missing identity fails the service's identity check.
func PrincipalFromContext(ctx context.Context) Principal {
	identity := auth.IdentityFromContext(ctx)
	return Principal{
		Subject: identity.UserID, Username: identity.Username, Email: identity.Email,
		Name: strings.TrimSpace(identity.GivenName + " " + identity.FamilyName), Roles: identity.Roles,
	}
}
