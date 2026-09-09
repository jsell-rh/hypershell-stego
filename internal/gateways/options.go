package gateways

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"unicode/utf8"
)

// Options supplies trusted placement settings and controller subjects.
// Subjects belong to the issuer configured in the token verifier.
type Options struct {
	ControlPlaneSubjects []string
	DatabaseProvider     string
}

const ProviderDeployment = "deployment"
const ProviderCNPG = "cnpg"

func resolveDatabaseProvider(raw string) (string, error) {
	switch raw {
	case "", ProviderDeployment:
		return ProviderDeployment, nil
	case ProviderCNPG:
		return ProviderCNPG, nil
	default:
		return "", errors.New("DATABASE_PROVIDER must be deployment or cnpg")
	}
}

func OptionsFromEnvironment() (Options, error) {
	var options Options
	var err error
	options.DatabaseProvider, err = resolveDatabaseProvider(os.Getenv("DATABASE_PROVIDER"))
	if err != nil {
		return Options{}, err
	}
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
