// Package config parses Slice 06 Scenario Manager startup configuration from
// the environment. Slice 06 introduces a single tunable: the number of
// concurrent runner-start workers. The observation scheduler uses a fixed four
// workers and is not configurable. A malformed, zero, negative, or
// out-of-range worker count is a fatal startup error: it must fail SM startup
// before scenario selection or runner-start discovery begins.
package config

import (
	"fmt"
	"os"
	"strconv"
)

// RunnerStartWorkersEnv names the environment variable that configures the
// runner-start worker count.
const RunnerStartWorkersEnv = "SCENARIO_MANAGER_RUNNER_START_WORKERS"

// MinRunnerStartWorkers and MaxRunnerStartWorkers bound the accepted worker
// count. The contract accepts 1, 4, and 64; 0, negatives, and values above 64
// fail startup.
const (
	MinRunnerStartWorkers = 1
	MaxRunnerStartWorkers = 64
)

// DefaultRunnerStartWorkers is the count used when the variable is absent.
const DefaultRunnerStartWorkers = 4

// RunnerStartWorkers reads SCENARIO_MANAGER_RUNNER_START_WORKERS from the
// process environment and validates it. An absent variable resolves to 4.
func RunnerStartWorkers() (int, error) {
	return ParseRunnerStartWorkers(os.Getenv(RunnerStartWorkersEnv))
}

// ParseRunnerStartWorkers validates a raw environment value. An empty value
// (absent variable) resolves to the default of 4. The value must be a base-10
// integer with no surrounding whitespace, in [MinRunnerStartWorkers,
// MaxRunnerStartWorkers]. Leading sign characters other than a digit are
// rejected so that "+4" is treated as malformed rather than accepted.
func ParseRunnerStartWorkers(value string) (int, error) {
	if value == "" {
		return DefaultRunnerStartWorkers, nil
	}
	if !isPureDecimal(value) {
		return 0, fmt.Errorf("%s: malformed worker count %q (must be a base-10 integer in [%d, %d])",
			RunnerStartWorkersEnv, value, MinRunnerStartWorkers, MaxRunnerStartWorkers)
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s: malformed worker count %q: %w",
			RunnerStartWorkersEnv, value, err)
	}
	if n < MinRunnerStartWorkers || n > MaxRunnerStartWorkers {
		return 0, fmt.Errorf("%s: worker count must be in [%d, %d], got %d",
			RunnerStartWorkersEnv, MinRunnerStartWorkers, MaxRunnerStartWorkers, n)
	}
	return n, nil
}

// isPureDecimal reports whether s consists only of ASCII digits (no sign, no
// whitespace, no exponent). This makes "+4", "-1", " 4 ", "0x10", and "1.5"
// all malformed rather than partially accepted.
func isPureDecimal(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}
