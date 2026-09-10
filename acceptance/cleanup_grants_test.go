package acceptance

import (
	"encoding/json"
	"strings"
	"testing"

	auth "github.com/jsell-rh/hypershell-stego/out/auth"
)

func cleanupGrant(subject, resource, owner, target string) auth.Grant {
	return auth.Grant{Subject: subject, Resource: resource, Operation: "cleanup." + owner, Target: target}
}

func withCleanupGrants(t testing.TB, settings []string, grants ...auth.Grant) []string {
	t.Helper()
	return withExactGrants(t, "HYPERSHELL_CLEANUP_GRANTS", settings, grants...)
}

func writeGrant(subject, operation, target string) auth.Grant {
	return auth.Grant{Subject: subject, Resource: "Gateway", Operation: operation, Target: target}
}

func databaseWriteGrant(subject, provider string) auth.Grant {
	return auth.Grant{Subject: subject, Resource: "ManagedDatabase", Operation: "observe.provider", Target: provider}
}

func withControllerWriteGrants(t testing.TB, settings []string, grants ...auth.Grant) []string {
	t.Helper()
	return withExactGrants(t, "HYPERSHELL_CONTROLLER_WRITE_GRANTS", settings, grants...)
}

func withExactGrants(t testing.TB, name string, settings []string, grants ...auth.Grant) []string {
	t.Helper()
	issuer := ""
	for _, setting := range settings {
		if value, ok := strings.CutPrefix(setting, "STEGO_AUTH_ISSUER="); ok {
			issuer = value
		}
	}
	if issuer == "" {
		t.Fatal("grants require the API issuer")
	}
	for i := range grants {
		if grants[i].Issuer == "" {
			grants[i].Issuer = issuer
		}
	}
	if grants == nil {
		grants = []auth.Grant{}
	}
	data, err := json.Marshal(grants)
	if err != nil {
		t.Fatal(err)
	}
	return append(settings, name+"="+string(data))
}

func controllerWritePolicy(t testing.TB, issuer string, grants ...auth.Grant) *auth.GrantPolicy {
	t.Helper()
	for i := range grants {
		grants[i].Issuer = issuer
	}
	policy, err := auth.NewGrantPolicy(grants)
	if err != nil {
		t.Fatal(err)
	}
	return policy
}
