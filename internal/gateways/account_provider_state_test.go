package gateways

import (
	"errors"
	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	"testing"
)

func TestAccountProviderStateRequiresExactGrant(t *testing.T) {
	p := Principal{Issuer: "https://issuer.example", Subject: "account-worker", Roles: []string{"platform:admin"}}
	s := &Service{controlPlaneSubjects: map[string]bool{p.Subject: true}}
	if err := s.authorizeAccountProviderState(p); !errors.Is(err, ErrForbidden) {
		t.Fatal("broad control-plane identity allowed journal access")
	}
	for _, grant := range []auth.Grant{
		{Issuer: p.Issuer, Subject: p.Subject, Resource: "Gateway", Operation: "provider-state"},
		{Issuer: p.Issuer, Subject: p.Subject, Resource: "ServiceAccount", Operation: "cleanup.identity"},
		{Issuer: p.Issuer, Subject: p.Subject, Resource: "ServiceAccount", Operation: "provider-state", Target: "other"},
		{Issuer: "https://other.example", Subject: p.Subject, Resource: "ServiceAccount", Operation: "provider-state"},
	} {
		policy, err := auth.NewGrantPolicy([]auth.Grant{grant})
		if err != nil {
			t.Fatal(err)
		}
		s.providerStatePolicy = policy
		if err := s.authorizeAccountProviderState(p); !errors.Is(err, ErrForbidden) {
			t.Fatal("wrong grant allowed journal access")
		}
	}
	policy, err := auth.NewGrantPolicy([]auth.Grant{{Issuer: p.Issuer, Subject: p.Subject, Resource: "ServiceAccount", Operation: "provider-state"}})
	if err != nil {
		t.Fatal(err)
	}
	s.providerStatePolicy = policy
	if err = s.authorizeAccountProviderState(p); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []Principal{{Subject: p.Subject}, {Issuer: p.Issuer}, {Issuer: p.Issuer, Subject: "other"}, {Issuer: p.Issuer, Subject: p.Subject + " "}} {
		if err = s.authorizeAccountProviderState(invalid); !errors.Is(err, ErrForbidden) {
			t.Fatal("invalid machine identity allowed journal access")
		}
	}
	s.controlPlaneSubjects = nil
	if err = s.authorizeAccountProviderState(p); !errors.Is(err, ErrForbidden) {
		t.Fatal("non-controller identity allowed journal access")
	}
}
func TestAccountProviderStateConfiguration(t *testing.T) {
	for _, raw := range []string{"", `[]`} {
		t.Setenv("HYPERSHELL_PROVIDER_STATE_GRANTS", raw)
		opts, err := OptionsFromEnvironment()
		if err != nil {
			t.Fatal(err)
		}
		if opts.ProviderStatePolicy.Allows(auth.Identity{Issuer: "https://issuer.example", UserID: "worker"}, "ServiceAccount", "provider-state", "") {
			t.Fatal("empty configuration allowed access")
		}
	}
	for _, raw := range []string{`null`, `{}`, `[{"subject":"worker"}]`, `[{"issuer":"https://issuer.example","subject":"worker","resource":"ServiceAccount","operation":"provider-state","operation":"other"}]`} {
		t.Setenv("HYPERSHELL_PROVIDER_STATE_GRANTS", raw)
		if _, err := OptionsFromEnvironment(); err == nil {
			t.Fatal("invalid provider-state configuration accepted")
		}
	}
}
