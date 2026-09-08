package gateways

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"unicode/utf8"
)

// Options supplies subjects from the issuer configured in the token verifier.
// Only trusted application configuration can set this list.
type Options struct{ ControlPlaneSubjects []string }

func OptionsFromEnvironment() (Options, error) {
	var options Options
	raw := os.Getenv("HYPERSHELL_CONTROL_PLANE_SUBJECTS")
	if raw == "" {
		return options, nil
	}
	if len(raw) > 16384 || json.Unmarshal([]byte(raw), &options.ControlPlaneSubjects) != nil || strings.TrimSpace(raw) == "null" {
		return Options{}, errors.New("control-plane subjects must be a JSON array")
	}
	return options, validateSubjects(options.ControlPlaneSubjects)
}
func validateSubjects(subjects []string) error {
	if len(subjects) > 32 {
		return errors.New("too many control-plane subjects")
	}
	seen := map[string]bool{}
	for _, subject := range subjects {
		if subject == "" || strings.TrimSpace(subject) != subject || len(subject) > 512 || !utf8.ValidString(subject) || strings.ContainsRune(subject, 0) || seen[subject] {
			return errors.New("control-plane subject is invalid or repeated")
		}
		seen[subject] = true
	}
	return nil
}
func (s *Service) isControlPlane(principal Principal) bool {
	return s.controlPlaneSubjects[principal.Subject]
}
