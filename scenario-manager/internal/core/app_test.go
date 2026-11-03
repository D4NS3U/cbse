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

package core

import (
	"context"
	"log"
	"os"
	"os/exec"
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

// transitionRecorder records readiness transitions in a shared event slice
// so a test can order them against log output.
type transitionRecorder struct {
	events *[]string
}

func (r *transitionRecorder) Ready() {
	*r.events = append(*r.events, "ready")
}

func (r *transitionRecorder) NotReady() {
	*r.events = append(*r.events, "notready")
}

// orderedLogSink is an io.Writer that records each written log line in the
// shared event slice as "log:<line>", so a test can order log output against
// readiness transitions.
type orderedLogSink struct {
	events *[]string
}

func (o *orderedLogSink) Write(p []byte) (int, error) {
	for _, line := range strings.Split(string(p), "\n") {
		if line == "" {
			continue
		}
		*o.events = append(*o.events, "log:"+line)
	}
	return len(p), nil
}

// TestMarkReadyTransitionsReadinessBeforeReadyLog pins the ready
// transition ordering: markReady flips the explicit readiness state to ready
// strictly before emitting the existing "Scenario Manager is ready" log, so
// the /readyz 200 answer can never trail the ready log. RunScenarioManager
// calls markReady only after the last component start; this test pins the
// transition point itself.
func TestMarkReadyTransitionsReadinessBeforeReadyLog(t *testing.T) {
	var events []string
	recorder := &transitionRecorder{events: &events}
	previous := log.Writer()
	log.SetOutput(&orderedLogSink{events: &events})
	defer log.SetOutput(previous)

	markReady(recorder)

	if len(events) != 2 {
		t.Fatalf("events = %#v, want exactly [ready, log:*Scenario Manager is ready]", events)
	}
	if events[0] != "ready" {
		t.Fatalf("events = %#v, want Ready() strictly before the ready log", events)
	}
	if !strings.HasSuffix(events[1], " Scenario Manager is ready") || !strings.HasPrefix(events[1], "log:") {
		t.Fatalf("events = %#v, want the ready log line after Ready()", events)
	}
}

// TestMarkReadyAllowsNilReporter pins that a nil readiness reporter disables
// the transition without breaking the ready log emission.
func TestMarkReadyAllowsNilReporter(t *testing.T) {
	previous := log.Writer()
	sink := &orderedLogSink{events: &[]string{}}
	log.SetOutput(sink)
	defer log.SetOutput(previous)

	markReady(nil)

	if len(*sink.events) != 1 || !strings.HasSuffix((*sink.events)[0], " Scenario Manager is ready") || !strings.HasPrefix((*sink.events)[0], "log:") {
		t.Fatalf("events = %#v, want exactly the ready log line", *sink.events)
	}
}

// TestRunScenarioManagerStartupFailureSkipsReadyTransition pins the
// ready-after-start ordering from the failure side: a startup configuration
// failure exits fatal in the first validation class, before any informer,
// consumer, selector, or scheduler starts, so neither the ready log nor the
// readiness transition may happen. The fatal path runs in a re-executed
// child of this test binary because log.Fatalf exits the process.
func TestRunScenarioManagerStartupFailureSkipsReadyTransition(t *testing.T) {
	// Child mode: exercise the fatal startup path in-process. The invalid
	// worker count fails the first startup validation class; log.Fatalf
	// exits the child.
	if os.Getenv("CBSE_SM_READY_ORDER_PROBE") != "" {
		RunScenarioManager(context.Background(), nil)
		return
	}
	// Inherit the environment without any SCENARIO_MANAGER_* setting so the
	// injected malformed worker count is the only startup input.
	var env []string
	for _, kv := range os.Environ() {
		if strings.HasPrefix(kv, "SCENARIO_MANAGER_") {
			continue
		}
		env = append(env, kv)
	}
	cmd := exec.Command(os.Args[0], "-test.run=TestRunScenarioManagerStartupFailureSkipsReadyTransition")
	cmd.Env = append(env, "CBSE_SM_READY_ORDER_PROBE=1", "SCENARIO_MANAGER_RUNNER_START_WORKERS=0")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatal("child exited cleanly; want a fatal startup failure")
	}
	if !strings.Contains(string(out), "startup configuration error") {
		t.Fatalf("child output = %q, want the startup configuration failure class", out)
	}
	if strings.Contains(string(out), "Scenario Manager is ready") {
		t.Fatal("ready log emitted although startup validation failed")
	}
}
