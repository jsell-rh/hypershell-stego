package gateways

import (
	"strings"
	"testing"
)

func TestControlPlaneConfigurationFailsClosed(t *testing.T) {
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
