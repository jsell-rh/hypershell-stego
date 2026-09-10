package gateways

import (
	"encoding/json"

	auth "github.com/jsell-rh/hypershell-stego/out/auth"
)

// authorizeControllerWrite uses the stored placement before any field changes.
// Each request writes one field group. Other controller patches are denied.
func (s *Service) authorizeControllerWrite(p Principal, cluster string, patch PatchRequest, console *string) error {
	operation, target := "", ""
	rest := patch
	switch {
	case patch.Phase != nil || patch.Status != nil:
		if patch.Phase == nil || patch.Status == nil || console != nil {
			return ErrInvalid
		}
		operation, target = "observe.workload", cluster
		rest.Phase, rest.Status = nil, nil
	case patch.OIDC != nil:
		if console != nil {
			return ErrInvalid
		}
		operation = "configure.identity"
		rest.OIDC = nil
	case console != nil:
		operation, target = "configure.console", cluster
	default:
		return ErrForbidden
	}
	data, err := json.Marshal(rest)
	if err != nil || string(data) != "{}" {
		return ErrInvalid
	}
	return s.AuthorizeControllerWrite(p, "Gateway", operation, target)
}

// AuthorizeControllerWrite checks the verified caller against an exact grant.
func (s *Service) AuthorizeControllerWrite(p Principal, resource, operation, target string) error {
	if err := validatePrincipal(p); err != nil {
		return err
	}
	if !s.isControlPlane(p) || !s.controllerWritePolicy.Allows(auth.Identity{Issuer: p.Issuer, UserID: p.Subject}, resource, operation, target) {
		return ErrForbidden
	}
	return nil
}
