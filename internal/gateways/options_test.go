package gateways

import (
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
