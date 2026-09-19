package serviceaccounts

import (
	"errors"
	"os"
	"strconv"
)

// Limits are operator policy. Capacity targets do not restrict these values.
// Both quotas count active reservations as well as ready accounts.
type Limits struct {
	PerGateway int64
	PerCreator int64
}

func DefaultLimits() Limits { return Limits{PerGateway: 100, PerCreator: 10} }

func (l Limits) validate() error {
	if l.PerGateway < 1 || l.PerCreator < 1 || l.PerCreator > l.PerGateway {
		return errors.New("service-account quotas must be positive; creator quota must not exceed Gateway quota")
	}
	return nil
}

func LimitsFromEnvironment() (Limits, error) {
	limits := DefaultLimits()
	for _, setting := range []struct {
		name  string
		value *int64
	}{
		{"HYPERSHELL_SERVICE_ACCOUNT_GATEWAY_QUOTA", &limits.PerGateway},
		{"HYPERSHELL_SERVICE_ACCOUNT_CREATOR_QUOTA", &limits.PerCreator},
	} {
		if raw, exists := os.LookupEnv(setting.name); exists {
			value, err := strconv.ParseInt(raw, 10, 64)
			if err != nil || value < 1 || strconv.FormatInt(value, 10) != raw {
				return Limits{}, errors.New(setting.name + " must be a positive decimal integer")
			}
			*setting.value = value
		}
	}
	return limits, limits.validate()
}
