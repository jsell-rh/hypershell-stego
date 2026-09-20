package sandboxcountapp

import (
	"context"
	"errors"
	"strings"
	"testing"

	settings "github.com/jsell-rh/hypershell-stego/out/configuration"
)

// No certificate or token file exists. Invalid scalar settings must fail first.
func TestCounterConfigurationFailsBeforeProviderSetup(t *testing.T) {
	for name, value := range map[string]string{
		"HYPERSHELL_SANDBOX_COUNT_WATCH_LIMIT": "private-counter-value",
		"HYPERSHELL_SANDBOX_COUNT_RESYNC":      "6m",
	} {
		t.Run(name, func(t *testing.T) {
			for key, value := range map[string]string{
				"HYPERSHELL_API_GRPC_ADDR":                 "localhost:443",
				"HYPERSHELL_API_CA_FILE":                   "/absent-api-ca",
				"HYPERSHELL_API_TOKEN_FILE":                "/absent-api-token",
				"HYPERSHELL_KUBERNETES_URL":                "https://localhost:6443",
				"HYPERSHELL_KUBERNETES_CA_FILE":            "",
				"HYPERSHELL_KUBERNETES_TOKEN_FILE":         "/absent-cluster-token",
				"HYPERSHELL_CONTROL_NAMESPACE":             "control",
				"HYPERSHELL_MANAGED_CLUSTER_ID":            "fixture",
				"HYPERSHELL_GATEWAY_SANDBOX_RUNTIME_CLASS": "",
				"HYPERSHELL_SANDBOX_COUNT_WATCH_LIMIT":     "0",
				"HYPERSHELL_SANDBOX_COUNT_RESYNC":          "0s",
			} {
				t.Setenv(key, value)
			}
			t.Setenv(name, value)
			err := Run(context.Background(), nil)
			var detail *settings.Error
			if !errors.Is(err, settings.ErrConfiguration) || !errors.As(err, &detail) || detail.Group() != "SandboxCounter" {
				t.Fatal("invalid scalar settings reached provider setup", err)
			}
			if strings.Contains(err.Error(), value) || strings.Contains(err.Error(), "/absent") {
				t.Fatal("configuration error exposed input")
			}
		})
	}
}
