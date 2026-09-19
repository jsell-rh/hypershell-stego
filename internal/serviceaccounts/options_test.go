package serviceaccounts

import (
	"os"
	"strconv"
	"testing"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
)

func TestServiceAccountOptions(t *testing.T) {
	const name = "HYPERSHELL_SERVICE_ACCOUNT_CLEANUP_WORKERS"
	for _, key := range []string{name, "HYPERSHELL_SERVICE_ACCOUNT_GATEWAY_QUOTA", "HYPERSHELL_SERVICE_ACCOUNT_CREATOR_QUOTA"} {
		t.Setenv(key, "")
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
	if options, err := OptionsFromEnvironment(); err != nil || options != DefaultOptions() {
		t.Fatal("default worker policy changed", options, err)
	}
	for _, tc := range []struct {
		value string
		valid bool
	}{
		{"1", true}, {"8", true}, {strconv.Itoa(runtime.MaxParallelCycleWorkers), true},
		{"", false}, {"0", false}, {"-1", false}, {"08", false}, {"+8", false},
		{"8 ", false}, {"1e1", false}, {"9223372036854775808", false},
		{strconv.Itoa(runtime.MaxParallelCycleWorkers + 1), false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			t.Setenv(name, tc.value)
			options, err := OptionsFromEnvironment()
			if (err == nil) != tc.valid {
				t.Fatal("incorrect worker setting validation", options, err)
			}
		})
	}
	t.Setenv(name, "8")
	t.Setenv("HYPERSHELL_SERVICE_ACCOUNT_GATEWAY_QUOTA", "5000")
	t.Setenv("HYPERSHELL_SERVICE_ACCOUNT_CREATOR_QUOTA", "1000")
	options, err := OptionsFromEnvironment()
	if err != nil || options.Limits != (Limits{PerGateway: 5000, PerCreator: 1000}) || options.CleanupWorkers != 8 {
		t.Fatal("worker setting changed quota policy", options, err)
	}
	t.Setenv("HYPERSHELL_SERVICE_ACCOUNT_CREATOR_QUOTA", "5001")
	if _, err := OptionsFromEnvironment(); err == nil {
		t.Fatal("worker setting bypassed quota validation")
	}
}
