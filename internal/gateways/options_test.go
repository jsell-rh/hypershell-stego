package gateways

import (
	"errors"
	auth "github.com/jsell-rh/hypershell-stego/out/auth"
	"strings"
	"testing"
)

func TestControlPlaneConfigurationFailsClosed(t *testing.T) {
	t.Setenv("DATABASE_PROVIDER", "")
	for _, raw := range []string{"", `[]`, `["subject-a","subject-b"]`} {
		t.Setenv("HYPERSHELL_CONTROL_PLANE_SUBJECTS", raw)
		options, err := OptionsFromEnvironment()
		if err != nil {
			t.Fatalf("valid subject configuration: %v", err)
		}
		expected := 0
		if strings.Contains(raw, "subject-a") {
			expected = 2
		}
		if len(options.ControlPlaneSubjects) != expected {
			t.Fatal("subject configuration changed")
		}
	}
	for _, raw := range []string{`null`, `{}`, `[null]`, `[""]`, `["subject","subject"]`, `[" subject"]`, `["subject "]`, `["bad\u0000subject"]`, `[1]`, `["subject"] true`, `["` + strings.Repeat("x", 513) + `"]`, strings.Repeat(" ", 16385)} {
		t.Setenv("HYPERSHELL_CONTROL_PLANE_SUBJECTS", raw)
		if _, err := OptionsFromEnvironment(); err == nil {
			t.Fatalf("invalid configuration accepted: %q", raw)
		}
	}
	if err := validateSubjects(make([]string, 33)); err == nil {
		t.Fatal("too many subjects accepted")
	}
}

func TestDatabaseProviderConfiguration(t *testing.T) {
	t.Setenv("HYPERSHELL_CONTROL_PLANE_SUBJECTS", "")
	for _, value := range []string{"", ProviderDeployment, ProviderCNPG} {
		t.Setenv("DATABASE_PROVIDER", value)
		options, err := OptionsFromEnvironment()
		want := value
		if want == "" {
			want = ProviderDeployment
		}
		if err != nil || options.DatabaseProvider != want {
			t.Fatal("database provider", value, options, err)
		}
	}
	for _, value := range []string{"postgres", "CNPG", " deployment", "cnpg ", "deployment,cnpg"} {
		t.Setenv("DATABASE_PROVIDER", value)
		if _, err := OptionsFromEnvironment(); err == nil {
			t.Fatal("invalid provider accepted", value)
		}
	}
}

func TestCleanupRequiresAnExactGrant(t *testing.T) {
	caller := Principal{Issuer: "https://issuer.example", Subject: "worker", Username: "worker", Roles: []string{"platform:admin"}}
	service := &Service{controlPlaneSubjects: map[string]bool{"worker": true}}
	if err := service.AuthorizeCleanup(caller, "Gateway", "workload", "cluster-a"); !errors.Is(err, ErrForbidden) {
		t.Fatal("broad controller access granted cleanup", err)
	}
	policy, err := auth.NewGrantPolicy([]auth.Grant{{Issuer: caller.Issuer, Subject: caller.Subject, Resource: "Gateway", Operation: "cleanup.workload", Target: "cluster-a"}})
	if err != nil {
		t.Fatal(err)
	}
	service.cleanupPolicy = policy
	if err := service.AuthorizeCleanup(caller, "Gateway", "workload", "cluster-a"); err != nil {
		t.Fatal(err)
	}
	for _, scope := range [][3]string{{"Gateway", "workload", "cluster-b"}, {"Gateway", "identity", ""}, {"ManagedDatabase", "provider", ""}} {
		if err := service.AuthorizeCleanup(caller, scope[0], scope[1], scope[2]); !errors.Is(err, ErrForbidden) {
			t.Fatal("cleanup grant crossed its scope", err)
		}
	}
	caller.Issuer = "https://other.example"
	if err := service.AuthorizeCleanup(caller, "Gateway", "workload", "cluster-a"); !errors.Is(err, ErrForbidden) {
		t.Fatal("subject crossed its issuer", err)
	}
}

func TestCleanupConfigurationDoesNotFallBackToControllerAccess(t *testing.T) {
	t.Setenv("DATABASE_PROVIDER", "")
	t.Setenv("HYPERSHELL_CONTROL_PLANE_SUBJECTS", `["worker"]`)
	for _, raw := range []string{"", `[]`} {
		t.Setenv("HYPERSHELL_CLEANUP_GRANTS", raw)
		options, err := OptionsFromEnvironment()
		if err != nil {
			t.Fatal(err)
		}
		if options.CleanupPolicy.Allows(auth.Identity{Issuer: "https://issuer.example", UserID: "worker"}, "Gateway", "cleanup.workload", "cluster-a") {
			t.Fatal("missing grant allowed cleanup")
		}
	}
	for _, raw := range []string{`null`, `{}`, `[{"subject":"worker"}]`, `[{"issuer":"https://issuer.example","subject":"worker","resource":"Gateway","operation":"cleanup.workload","target":"a","target":"b"}]`} {
		t.Setenv("HYPERSHELL_CLEANUP_GRANTS", raw)
		if _, err := OptionsFromEnvironment(); err == nil {
			t.Fatal("invalid cleanup policy was accepted")
		}
	}
}

func TestControllerWriteConfigurationFailsClosed(t *testing.T) {
	t.Setenv("DATABASE_PROVIDER", "")
	t.Setenv("HYPERSHELL_CONTROL_PLANE_SUBJECTS", `["worker"]`)
	t.Setenv("HYPERSHELL_CLEANUP_GRANTS", `[{"issuer":"https://issuer.example","subject":"worker","resource":"Gateway","operation":"observe.workload","target":"a"}]`)
	for _, raw := range []string{"", `[]`} {
		t.Setenv("HYPERSHELL_CONTROLLER_WRITE_GRANTS", raw)
		options, err := OptionsFromEnvironment()
		if err != nil {
			t.Fatal(err)
		}
		if options.ControllerWritePolicy.Allows(auth.Identity{Issuer: "https://issuer.example", UserID: "worker"}, "Gateway", "observe.workload", "a") {
			t.Fatal("missing write grant used another policy")
		}
	}
	for _, raw := range []string{`null`, `{}`, `[{"subject":"worker"}]`, `[{"issuer":"https://issuer.example","subject":"worker","resource":"Gateway","operation":"observe.workload","target":"a","target":"b"}]`} {
		t.Setenv("HYPERSHELL_CONTROLLER_WRITE_GRANTS", raw)
		if _, err := OptionsFromEnvironment(); err == nil {
			t.Fatal("invalid write policy was accepted")
		}
	}
}
