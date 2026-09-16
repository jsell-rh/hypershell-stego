package gateways

import (
	"errors"
	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	"github.com/segmentio/ksuid"
	"testing"
)

func TestConsoleProviderStateReadGrantIsReadOnly(t *testing.T) {
	id := ksuid.New().String()
	p := Principal{Issuer: "https://issuer.example", Subject: "console-provisioner", Username: "console-provisioner", Roles: []string{"platform:admin"}}
	s := &Service{controlPlaneSubjects: map[string]bool{p.Subject: true}}
	grant := auth.Grant{Issuer: p.Issuer, Subject: p.Subject, Resource: "Gateway", Operation: "read.console-client"}
	for _, wrong := range []auth.Grant{{Issuer: p.Issuer, Subject: p.Subject, Resource: "Gateway", Operation: "provider-state"}, {Issuer: "https://other.example", Subject: p.Subject, Resource: "Gateway", Operation: grant.Operation}, {Issuer: p.Issuer, Subject: p.Subject, Resource: "Gateway", Operation: grant.Operation, Target: id}} {
		policy, err := auth.NewGrantPolicy([]auth.Grant{wrong})
		if err != nil {
			t.Fatal(err)
		}
		s.providerStatePolicy = policy
		if err := s.authorizeIdentityProviderStateRead(p, id, consoleIdentityProviderStateScope); !errors.Is(err, ErrForbidden) {
			t.Fatal("wrong grant permitted console state", err)
		}
	}
	policy, err := auth.NewGrantPolicy([]auth.Grant{grant})
	if err != nil {
		t.Fatal(err)
	}
	s.providerStatePolicy = policy
	if err := s.authorizeIdentityProviderStateRead(p, id, consoleIdentityProviderStateScope); err != nil {
		t.Fatal(err)
	}
	if err := s.authorizeIdentityProviderStateRead(p, id, identityProviderStateScope); !errors.Is(err, ErrForbidden) {
		t.Fatal("console reader reached native state", err)
	}
	if err := s.authorizeIdentityController(p, id); !errors.Is(err, ErrForbidden) {
		t.Fatal("console reader acquired state write access", err)
	}
	if err := s.AuthorizeCleanup(p, "Gateway", "identity", ""); !errors.Is(err, ErrForbidden) {
		t.Fatal("console reader acquired cleanup access", err)
	}
	s.controlPlaneSubjects = nil
	if err := s.authorizeIdentityProviderStateRead(p, id, consoleIdentityProviderStateScope); !errors.Is(err, ErrForbidden) {
		t.Fatal("non-controller read console state", err)
	}
}
