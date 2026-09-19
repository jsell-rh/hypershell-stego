package serviceaccounts

import (
	"errors"
	"os"
	"strconv"

	runtime "github.com/jsell-rh/hypershell-stego/out/controller"
)

// Options are copied at startup. Cleanup workers are separate from account quotas.
type Options struct {
	Limits         Limits
	CleanupWorkers int
}

// DefaultOptions selects the initial API worker policy. Existing constructors
// retain one worker; callers select parallel work with NewWithOptions.
func DefaultOptions() Options { return Options{Limits: DefaultLimits(), CleanupWorkers: 8} }

func (o Options) validate() error {
	if err := o.Limits.validate(); err != nil {
		return err
	}
	if o.CleanupWorkers < 1 || o.CleanupWorkers > runtime.MaxParallelCycleWorkers {
		return errors.New("service-account cleanup workers must be between 1 and " + strconv.Itoa(runtime.MaxParallelCycleWorkers))
	}
	return nil
}

func OptionsFromEnvironment() (Options, error) {
	options := DefaultOptions()
	limits, err := LimitsFromEnvironment()
	if err != nil {
		return Options{}, err
	}
	options.Limits = limits
	const name = "HYPERSHELL_SERVICE_ACCOUNT_CLEANUP_WORKERS"
	if raw, exists := os.LookupEnv(name); exists {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || value < 1 || value > int64(runtime.MaxParallelCycleWorkers) || strconv.FormatInt(value, 10) != raw {
			return Options{}, errors.New(name + " must be a decimal integer between 1 and " + strconv.Itoa(runtime.MaxParallelCycleWorkers))
		}
		options.CleanupWorkers = int(value)
	}
	return options, options.validate()
}
