package acceptance

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jsell-rh/hypershell-stego/internal/gatewayidentityapp"
	"github.com/jsell-rh/hypershell-stego/internal/gatewayworkloadapp"
	"github.com/jsell-rh/hypershell-stego/internal/serviceaccountprovisioner"
	settings "github.com/jsell-rh/hypershell-stego/out/configuration"
)

// Invalid configuration must fail before certificate, token, or provider setup.
func TestControlConfigurationFailsBeforeProviderSetup(t *testing.T) {
	workers := []struct {
		name string
		open func(context.Context) error
	}{
		{"identity", func(ctx context.Context) error { return gatewayidentityapp.Run(ctx, nil) }},
		{"workload", func(ctx context.Context) error { return gatewayworkloadapp.Run(ctx, nil) }},
		{"provisioner", func(ctx context.Context) error {
			app, err := serviceaccountprovisioner.Open(ctx)
			if app != nil {
				app.Close()
				return errors.New("invalid configuration returned an application")
			}
			return err
		}},
	}
	for _, worker := range workers {
		for _, field := range []struct{ name, env string }{
			{"Address", "HYPERSHELL_API_GRPC_ADDR"},
			{"CAFile", "HYPERSHELL_API_CA_FILE"},
			{"TokenFile", "HYPERSHELL_API_TOKEN_FILE"},
		} {
			t.Run(worker.name+"/"+field.name, func(t *testing.T) {
				absent := filepath.Join(t.TempDir(), "absent")
				t.Setenv("HYPERSHELL_API_GRPC_ADDR", "localhost:443")
				t.Setenv("HYPERSHELL_API_CA_FILE", absent)
				t.Setenv("HYPERSHELL_API_TOKEN_FILE", absent)
				t.Setenv("HYPERSHELL_PROVISIONER_SUBJECTS", `["fixture"]`)
				private := "private-setting-" + field.name + "\n"
				t.Setenv(field.env, private)
				err := worker.open(context.Background())
				var detail *settings.Error
				if !errors.Is(err, settings.ErrConfiguration) || !errors.As(err, &detail) || detail.Group() != "ControlAPI" || detail.Field() != field.name {
					t.Fatal("invalid configuration reached provider setup")
				}
				if strings.Contains(err.Error(), "private-setting") || strings.Contains(err.Error(), absent) {
					t.Fatal("configuration error exposed input")
				}
			})
		}
	}
}

func TestWorkloadClusterConfigurationPrecedesConnectionSetup(t *testing.T) {
	absent := filepath.Join(t.TempDir(), "absent")
	for key, value := range map[string]string{
		"HYPERSHELL_API_GRPC_ADDR":                 "localhost:443",
		"HYPERSHELL_API_CA_FILE":                   absent,
		"HYPERSHELL_API_TOKEN_FILE":                absent,
		"HYPERSHELL_KUBERNETES_URL":                "https://localhost:6443",
		"HYPERSHELL_KUBERNETES_CA_FILE":            "",
		"HYPERSHELL_KUBERNETES_TOKEN_FILE":         absent,
		"HYPERSHELL_CONTROL_NAMESPACE":             "private-namespace\n",
		"HYPERSHELL_MANAGED_CLUSTER_ID":            "fixture",
		"HYPERSHELL_GATEWAY_SANDBOX_RUNTIME_CLASS": "",
	} {
		t.Setenv(key, value)
	}
	err := gatewayworkloadapp.Run(context.Background(), nil)
	var detail *settings.Error
	if !errors.Is(err, settings.ErrConfiguration) || !errors.As(err, &detail) || detail.Group() != "ClusterWorker" || detail.Field() != "ControlNamespace" {
		t.Fatal("invalid cluster configuration reached connection setup")
	}
	if strings.Contains(err.Error(), "private-namespace") || strings.Contains(err.Error(), absent) {
		t.Fatal("configuration error exposed input")
	}
}
