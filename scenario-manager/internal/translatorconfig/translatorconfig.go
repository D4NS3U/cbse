// Package translatorconfig centralizes translator-workflow configuration that
// must be shared by multiple packages without creating import cycles.
//
// Both the core handoff logic and the concrete NATS adapter need the same retry
// and unpublished-claim recovery settings. Keeping the parsing here lets those
// packages stay decoupled from each other while still agreeing on defaults and
// validation behavior.
package translatorconfig

import (
	"log"
	"os"
	"strconv"
	"time"
)

const (
	// MaxAttemptsEnv configures how many translation claims a scenario may
	// consume before Scenario Manager marks it as Failed.
	MaxAttemptsEnv = "SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS"
	// PublishRecoveryTimeoutEnv configures how long an unpublished Scheduled
	// claim may remain untouched before higher-level orchestration can recover it.
	PublishRecoveryTimeoutEnv = "SCENARIO_MANAGER_TRANS_PUBLISH_RECOVERY_TIMEOUT"
	// DefaultMaxAttempts is the v1 fallback when the environment is unset or
	// invalid.
	DefaultMaxAttempts = 3
	// DefaultPublishRecoveryTimeout is the v1 fallback for unpublished-claim
	// recovery windows when the environment is unset or invalid.
	DefaultPublishRecoveryTimeout = time.Minute
)

// LoadMaxAttempts parses the shared translator attempt limit from the
// environment.
//
// The value must be a positive integer. Invalid or non-positive values are
// logged and replaced with DefaultMaxAttempts so both core and transport layers
// continue using the same deterministic fallback.
func LoadMaxAttempts() int {
	raw := os.Getenv(MaxAttemptsEnv)
	if raw == "" {
		return DefaultMaxAttempts
	}

	parsed, err := strconv.Atoi(raw)
	switch {
	case err != nil:
		log.Printf("Invalid %s value %q; using default %d.", MaxAttemptsEnv, raw, DefaultMaxAttempts)
	case parsed <= 0:
		log.Printf("%s must be positive; using default %d.", MaxAttemptsEnv, DefaultMaxAttempts)
	default:
		return parsed
	}

	return DefaultMaxAttempts
}

// LoadPublishRecoveryTimeout parses the shared unpublished-claim recovery
// timeout from the environment.
//
// The value must be a positive Go duration string accepted by
// time.ParseDuration. Invalid or non-positive values are logged and replaced
// with DefaultPublishRecoveryTimeout so recovery behavior remains predictable.
func LoadPublishRecoveryTimeout() time.Duration {
	raw := os.Getenv(PublishRecoveryTimeoutEnv)
	if raw == "" {
		return DefaultPublishRecoveryTimeout
	}

	parsed, err := time.ParseDuration(raw)
	if err != nil || parsed <= 0 {
		log.Printf("Invalid %s value %q; using default %s.", PublishRecoveryTimeoutEnv, raw, DefaultPublishRecoveryTimeout)
		return DefaultPublishRecoveryTimeout
	}

	return parsed
}
