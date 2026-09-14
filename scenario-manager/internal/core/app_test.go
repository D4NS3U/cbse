package core

import (
	"strings"
	"testing"

	"github.com/D4NS3U/cbse/scenario-manager/internal/config"
)

// Env var name literals for the messaging template/stream-name checks (the
// names are unexported in package messaging).
const (
	edsAvailableTemplateEnv = "SCENARIO_MANAGER_EDS_AVAILABLE_SUBJECT_TEMPLATE"
	edsStreamNameEnv        = "SCENARIO_MANAGER_EDS_STREAM_NAME"
)

// envMap returns a getenv function over a map, defaulting missing keys to "".
func envMap(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

// baseEnv returns a getenv with valid alpha4 defaults so each test mutates one
// setting to isolate a failure class.
func baseEnv() map[string]string {
	return map[string]string{
		// Valid runner-start workers (default 4).
		// (absent resolves to default; set explicitly to "" is invalid)
		// NATS authentication-free.
		natsURLEnv: "nats://nats:4222",
		// Core DB credentials present (form only; not opened by these tests).
		coreDBDSNEnv:      "postgres://db:5432/cbse",
		coreDBUserEnv:     "cbse",
		coreDBPasswordEnv: "secret",
	}
}

func TestValidatePureConfigAcceptsDefaults(t *testing.T) {
	cfg, err := validatePureConfig(envMap(baseEnv()))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.workers != 4 {
		t.Fatalf("workers = %d; want default 4", cfg.workers)
	}
}

func TestValidatePureConfigRejectsMalformedWorkers(t *testing.T) {
	env := baseEnv()
	env[config.RunnerStartWorkersEnv] = "notanumber"
	if _, err := validatePureConfig(envMap(env)); err == nil {
		t.Fatal("malformed worker count: want error")
	}
}

func TestValidatePureConfigRejectsZeroWorkers(t *testing.T) {
	env := baseEnv()
	env[config.RunnerStartWorkersEnv] = "0"
	if _, err := validatePureConfig(envMap(env)); err == nil {
		t.Fatal("zero worker count: want error")
	}
}

func TestValidatePureConfigRejectsTooManyWorkers(t *testing.T) {
	env := baseEnv()
	env[config.RunnerStartWorkersEnv] = "65"
	if _, err := validatePureConfig(envMap(env)); err == nil {
		t.Fatal("worker count > 64: want error")
	}
}

func TestValidatePureConfigRejectsNonCanonicalTemplate(t *testing.T) {
	env := baseEnv()
	env[edsAvailableTemplateEnv] = "cbse.{namespace}.eds.scenarios.available" // missing {project}
	if _, err := validatePureConfig(envMap(env)); err == nil {
		t.Fatal("non-canonical template: want error")
	}
}

func TestValidatePureConfigRejectsNonCanonicalStreamName(t *testing.T) {
	env := baseEnv()
	env[edsStreamNameEnv] = "wrong_stream_name"
	if _, err := validatePureConfig(envMap(env)); err == nil {
		t.Fatal("non-canonical stream name: want error")
	}
}

func TestValidateNATSConfigAcceptsAuthenticationFree(t *testing.T) {
	env := baseEnv()
	url, err := validateNATSConfig(envMap(env))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if url != env[natsURLEnv] {
		t.Fatalf("url = %q; want %q", url, env[natsURLEnv])
	}
}

func TestValidateNATSConfigRejectsMissingURL(t *testing.T) {
	env := baseEnv()
	delete(env, natsURLEnv)
	if _, err := validateNATSConfig(envMap(env)); err == nil {
		t.Fatal("missing NATS URL: want error")
	}
}

func TestValidateNATSConfigRejectsUserInfoInURL(t *testing.T) {
	env := baseEnv()
	env[natsURLEnv] = "nats://user:pass@nats:4222"
	if _, err := validateNATSConfig(envMap(env)); err == nil {
		t.Fatal("credential-bearing NATS URL: want error")
	}
}

func TestValidateNATSConfigRejectsLegacyUser(t *testing.T) {
	env := baseEnv()
	env[natsUserEnv] = "legacy-user"
	if _, err := validateNATSConfig(envMap(env)); err == nil {
		t.Fatal("legacy NATS user: want error")
	}
}

func TestValidateNATSConfigRejectsLegacyPassword(t *testing.T) {
	env := baseEnv()
	env[natsPasswordEnv] = "legacy-pass"
	if _, err := validateNATSConfig(envMap(env)); err == nil {
		t.Fatal("legacy NATS password: want error")
	}
}

func TestValidateNATSConfigRejectsMalformedURL(t *testing.T) {
	env := baseEnv()
	env[natsURLEnv] = "://bad"
	_, err := validateNATSConfig(envMap(env))
	if err == nil {
		t.Fatal("malformed NATS URL: want error")
	}
	if !strings.Contains(err.Error(), "malformed") {
		t.Fatalf("error = %v; want malformed classification", err)
	}
}

func TestBuildCoreDBConnStringMergesCredentials(t *testing.T) {
	got, err := buildCoreDBConnString("postgres://db:5432/cbse", "user", "pass")
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if !strings.Contains(got, "user:pass@db:5432") {
		t.Fatalf("merged DSN = %q; want credentials merged", got)
	}
}

func TestBuildCoreDBConnStringRejectsMalformed(t *testing.T) {
	if _, err := buildCoreDBConnString("://bad", "u", "p"); err == nil {
		t.Fatal("malformed DSN: want error")
	}
}
