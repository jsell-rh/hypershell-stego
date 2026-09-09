package gateways

import "slices"

// AuthorizeCatalog applies current verified roles and trusted controller subjects.
// Gateway ownership does not grant access to platform placement records.
func (s *Service) AuthorizeCatalog(p Principal, write bool) error {
	if err := validatePrincipal(p); err != nil {
		return err
	}
	if s.isControlPlane(p) || slices.Contains(p.Roles, "platform:admin") {
		return nil
	}
	if !write && slices.Contains(p.Roles, "gateway:creator") {
		return nil
	}
	return ErrForbidden
}
