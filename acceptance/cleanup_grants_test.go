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
	issuer := ""
	for _, setting := range settings {
		if value, ok := strings.CutPrefix(setting, "STEGO_AUTH_ISSUER="); ok {
			issuer = value
		}
	}
	if issuer == "" {
		t.Fatal("cleanup grants require the API issuer")
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
	return append(settings, "HYPERSHELL_CLEANUP_GRANTS="+string(data))
}
