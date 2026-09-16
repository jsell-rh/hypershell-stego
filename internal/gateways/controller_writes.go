package gateways

import (
	"encoding/json"

	auth "github.com/jsell-rh/hypershell-stego/out/auth"
)

// authorizeControllerWrite uses the stored placement before any field changes.
// A public workload observation can include its endpoint. Both grants are
// required. Configuration fields cannot be mixed with observations.
func (s *Service) authorizeControllerWrite(p Principal, cluster string, patch PatchRequest, console, route *string) error {
	operation, target := "", ""
	workload := patch.Phase != nil || patch.Status != nil
	rest := patch
	switch {
	case workload:
		if patch.Phase == nil || patch.Status == nil {
			return ErrInvalid
		}
		operation, target = "observe.workload", cluster
		rest.Phase, rest.Status = nil, nil
	case route != nil:
		if console != nil {
			return ErrInvalid
		}
		operation, target = "observe.endpoint", cluster
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
	if err := s.AuthorizeControllerWrite(p, "Gateway", operation, target); err != nil {
		return err
	}
	if workload && route != nil {
		if err := s.AuthorizeControllerWrite(p, "Gateway", "observe.endpoint", cluster); err != nil {
			return err
		}
	}
	if workload && console != nil {
		return s.AuthorizeControllerWrite(p, "Gateway", "configure.console", cluster)
	}
	return nil
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
