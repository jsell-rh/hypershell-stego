package serviceaccounts

import (
	"os"
	"testing"
)

func TestServiceAccountLimits(t *testing.T) {
	gateway := "HYPERSHELL_SERVICE_ACCOUNT_GATEWAY_QUOTA"
	creator := "HYPERSHELL_SERVICE_ACCOUNT_CREATOR_QUOTA"
	for _, name := range []string{gateway, creator} {
		t.Setenv(name, "")
		if err := os.Unsetenv(name); err != nil {
			t.Fatal(err)
		}
	}
	if limits, err := LimitsFromEnvironment(); err != nil || limits != DefaultLimits() {
		t.Fatal("default quota changed", limits, err)
	}
	for _, tc := range []struct {
		gateway, creator string
		valid            bool
	}{
		{"5000", "1000", true}, {"101", "101", true}, {"1", "1", true},
		{"0", "1", false}, {"10", "11", false}, {"100", "0", false},
		{"", "10", false}, {"100", "", false}, {"+100", "10", false},
		{"100", "01", false}, {"100", "-1", false}, {"100 ", "10", false},
		{"9223372036854775808", "10", false}, {"1e3", "10", false},
	} {
		t.Run(tc.gateway+"/"+tc.creator, func(t *testing.T) {
			t.Setenv(gateway, tc.gateway)
			t.Setenv(creator, tc.creator)
			limits, err := LimitsFromEnvironment()
			if (err == nil) != tc.valid {
				t.Fatal("incorrect quota validation", limits, err)
			}
		})
	}
}
