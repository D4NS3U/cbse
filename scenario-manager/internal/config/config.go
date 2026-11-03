// Copyright 2025-2026 Daniel Seufferth
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package config parses Scenario Manager startup configuration from the
// environment. The two tunables are the number of concurrent runner-start
// workers and the single release/experiment watch namespace. The observation
// scheduler uses a fixed four workers and is not configurable. A malformed,
// zero, negative, or out-of-range worker count, or an absent or malformed
// watch namespace, is a fatal startup error: it must fail SM startup before
// scenario selection, runner-start discovery, or any namespace-scoped
// authorization check begins.
package config

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

// RunnerStartWorkersEnv names the environment variable that configures the
// runner-start worker count.
const RunnerStartWorkersEnv = "SCENARIO_MANAGER_RUNNER_START_WORKERS"

// WatchNamespaceEnv names the environment variable that fixes the single
// release/experiment namespace the Scenario Manager watches. v0.1 is a
// single-namespace installation: the release namespace is both the
// control-plane namespace and the only experiment namespace, so the value is
// required (an absent or malformed value is a fatal startup error) and is
// used to scope the startup authorization checks and the experiment informer
// cache. The manifest smoke stack already injects the variable via the
// downward API, so consuming it is compatible with the existing manifests.
const WatchNamespaceEnv = "SCENARIO_MANAGER_WATCH_NAMESPACE"

// watchNamespaceLabelRe is a DNS label: the only shape a Kubernetes namespace
// can take.
var watchNamespaceLabelRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

// WatchNamespace reads SCENARIO_MANAGER_WATCH_NAMESPACE from the process
// environment and validates it.
func WatchNamespace() (string, error) {
	return ParseWatchNamespace(os.Getenv(WatchNamespaceEnv))
}

// ParseWatchNamespace validates a raw watch-namespace value. An empty value
// (absent variable) is a fatal startup error: v0.1 has no cluster-wide
// fallback. The value must be a single lowercase DNS label of at most 63
// characters with no surrounding whitespace; anything else is malformed.
func ParseWatchNamespace(value string) (string, error) {
	if value == "" {
		return "", fmt.Errorf("%s is not set", WatchNamespaceEnv)
	}
	if len(value) > 63 || !watchNamespaceLabelRe.MatchString(value) {
		return "", fmt.Errorf("%s is malformed: %q is not a DNS label", WatchNamespaceEnv, value)
	}
	return value, nil
}

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
