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

package config

import (
	"bytes"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/D4NS3U/cbse/component-templates/post-processing-service/internal/evaluation"
)

const testUID = "aabbccdd-1122-3344-5566-77889900aabb"

// setEnv sets the full valid PPS environment for the test experiment.
func setEnv(t *testing.T) {
	t.Helper()
	sets := map[string]string{
		"NATS_URL":                        "nats://127.0.0.1:4222",
		"PPS_STREAM":                      "cbse_pps",
		"PPS_REQUEST_SUBJECT":             "cbse.default.proj.pps.request",
		"PPS_EVALUATION_SUBJECT_TEMPLATE": "cbse.default.proj.pps.%s.evaluation",
		"PPS_CONSUMER":                    "pps-aabbccdd1122",
		"SIMULATIONPROJECTNAMESPACE":      "default",
		"SIMULATIONPROJECTNAME":           "proj",
		"SIMULATIONEXPERIMENTUID":         testUID,
	}
	for k, v := range sets {
		t.Setenv(k, v)
	}
}

// unsetEnv unsets a PPS environment variable.
func unsetEnv(t *testing.T, key string) {
	t.Helper()
	t.Setenv(key, "")
}

// writeDBDir writes the five Result DB connection Secret files to dir.
func writeDBDir(t *testing.T, dir, host, port string) {
	t.Helper()
	for k, v := range map[string]string{
		"host": host, "port": port, "dbname": "results", "user": "u", "password": "p",
	} {
		if err := os.WriteFile(filepath.Join(dir, k), []byte(v), 0o600); err != nil {
			t.Fatalf("write %s: %v", k, err)
		}
	}
}

// dbMounts returns a Mounts pointing at a temp dir with a valid secret.
func dbMounts(t *testing.T) Mounts {
	t.Helper()
	dir := t.TempDir()
	writeDBDir(t, dir, "db.example.com", "5432")
	return Mounts{ResultDBDir: dir}
}

func TestLoadValid(t *testing.T) {
	setEnv(t)
	cfg, err := Load(nil, dbMounts(t))
	if err != nil {
		t.Fatalf("Load err = %v", err)
	}
	if cfg.Namespace != "default" || cfg.Project != "proj" {
		t.Fatalf("ns/proj = %q/%q, want default/proj", cfg.Namespace, cfg.Project)
	}
	if cfg.ExperimentUID != testUID {
		t.Fatalf("uid = %q", cfg.ExperimentUID)
	}
	if cfg.Stream != "cbse_pps" || cfg.Consumer != "pps-aabbccdd1122" {
		t.Fatalf("stream/consumer = %q/%q", cfg.Stream, cfg.Consumer)
	}
	if cfg.Policy != evaluation.PolicyStatistical {
		t.Fatalf("policy = %q, want statistical (default)", cfg.Policy)
	}
	if cfg.DetAddRunners != 2 || cfg.MaxReplications != 10000 || cfg.MaxRunnersRound != 1000 {
		t.Fatalf("defaults = %d/%d/%d, want 2/10000/1000", cfg.DetAddRunners, cfg.MaxReplications, cfg.MaxRunnersRound)
	}
	if cfg.ResultDB.Host != "db.example.com" || cfg.ResultDB.Port != 5432 || cfg.ResultDB.DBName != "results" {
		t.Fatalf("result db = %+v", cfg.ResultDB)
	}
}

func TestLoadFlags(t *testing.T) {
	setEnv(t)
	args := []string{
		"-evaluation-policy", "deterministic-first-round-not-met",
		"-deterministic-additional-runners", "5",
		"-max-replications", "500",
		"-max-runners-per-round", "10",
	}
	cfg, err := Load(args, dbMounts(t))
	if err != nil {
		t.Fatalf("Load err = %v", err)
	}
	if cfg.Policy != evaluation.PolicyDeterministicFirstRoundNotMet {
		t.Fatalf("policy = %q", cfg.Policy)
	}
	if cfg.DetAddRunners != 5 || cfg.MaxReplications != 500 || cfg.MaxRunnersRound != 10 {
		t.Fatalf("knobs = %d/%d/%d, want 5/500/10", cfg.DetAddRunners, cfg.MaxReplications, cfg.MaxRunnersRound)
	}
}

// TestLoadMaxRunnersPerRoundZero is ruling Q7's 0-mode: 0 is accepted,
// carries the disabled state into the Config, and is logged at startup with
// an explicit observable line (the disabled state must be visible, not
// silent).
func TestLoadMaxRunnersPerRoundZero(t *testing.T) {
	setEnv(t)
	var buf bytes.Buffer
	prevOut, prevFlags := log.Writer(), log.Flags()
	log.SetOutput(&buf)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(prevOut)
		log.SetFlags(prevFlags)
	})
	cfg, err := Load([]string{"-max-runners-per-round", "0"}, dbMounts(t))
	if err != nil {
		t.Fatalf("Load err = %v", err)
	}
	if cfg.MaxRunnersRound != 0 {
		t.Fatalf("max runners per round = %d, want 0 (disabled)", cfg.MaxRunnersRound)
	}
	want := "pps: per-wave cap disabled"
	if !strings.Contains(buf.String(), want) {
		t.Fatalf("startup log = %q, want %q", buf.String(), want)
	}
}

// TestLoadMaxRunnersPerRoundNegative is ruling Q7's fail-fast: a negative
// -max-runners-per-round is a configuration error at startup.
func TestLoadMaxRunnersPerRoundNegative(t *testing.T) {
	setEnv(t)
	_, err := Load([]string{"-max-runners-per-round", "-1000"}, dbMounts(t))
	if err == nil {
		t.Fatal("negative max-runners-per-round accepted")
	}
	if !strings.Contains(err.Error(), "-max-runners-per-round") {
		t.Fatalf("error = %v, want mention of -max-runners-per-round", err)
	}
}

func TestLoadUnknownFlag(t *testing.T) {
	setEnv(t)
	_, err := Load([]string{"-bogus-flag"}, dbMounts(t))
	if err == nil {
		t.Fatal("unknown flag accepted")
	}
	if !strings.Contains(err.Error(), "bogus-flag") {
		t.Fatalf("error = %v, want mention of bogus-flag", err)
	}
}

func TestLoadUnknownPolicy(t *testing.T) {
	setEnv(t)
	for _, policy := range []string{"bogus", "Statistical", "deterministic"} {
		_, err := Load([]string{"-evaluation-policy", policy}, dbMounts(t))
		if err == nil {
			t.Fatalf("policy %q accepted", policy)
		}
	}
}

func TestLoadNonPositiveNumbers(t *testing.T) {
	setEnv(t)
	for _, args := range [][]string{
		{"-deterministic-additional-runners", "0"},
		{"-deterministic-additional-runners", "-1"},
		{"-max-replications", "0"},
		{"-max-replications", "-1"},
		{"-max-runners-per-round", "-1000"},
	} {
		if _, err := Load(args, dbMounts(t)); err == nil {
			t.Fatalf("args %v accepted", args)
		}
	}
}

func TestLoadMissingEnv(t *testing.T) {
	for _, key := range []string{
		"NATS_URL",
		"PPS_STREAM",
		"PPS_REQUEST_SUBJECT",
		"PPS_EVALUATION_SUBJECT_TEMPLATE",
		"PPS_CONSUMER",
		"SIMULATIONEXPERIMENTUID",
	} {
		t.Run(key, func(t *testing.T) {
			setEnv(t)
			unsetEnv(t, key)
			// The request subject is authoritative for ns/proj; to
			// exercise the identity env vars themselves it is unset.
			if key == "SIMULATIONPROJECTNAMESPACE" || key == "SIMULATIONPROJECTNAME" {
				unsetEnv(t, "PPS_REQUEST_SUBJECT")
			}
			if _, err := Load(nil, dbMounts(t)); err == nil {
				t.Fatalf("missing %s accepted", key)
			}
		})
	}
}

func TestLoadNATSURLWithUser(t *testing.T) {
	setEnv(t)
	t.Setenv("NATS_URL", "nats://robot:token@nats.example.com:4222")
	if _, err := Load(nil, dbMounts(t)); err == nil {
		t.Fatal("NATS_URL with user information accepted")
	}
	setEnv(t)
	t.Setenv("NATS_URL", "nats://")
	if _, err := Load(nil, dbMounts(t)); err == nil {
		t.Fatal("host-less NATS_URL accepted")
	}
}

func TestLoadBadRequestSubject(t *testing.T) {
	setEnv(t)
	for _, s := range []string{
		"cbse.default.proj.trans.request",
		"cbse.default.proj.pps.ready",
	} {
		t.Setenv("PPS_REQUEST_SUBJECT", s)
		if _, err := Load(nil, dbMounts(t)); err == nil {
			t.Fatalf("request subject %q accepted", s)
		}
	}
	// A request subject for a different namespace is consistent only when
	// the identity env and the evaluation template agree with it.
	t.Setenv("PPS_REQUEST_SUBJECT", "cbse.other.proj.pps.request")
	t.Setenv("SIMULATIONPROJECTNAMESPACE", "other")
	_, err := Load(nil, dbMounts(t))
	if err == nil {
		t.Fatal("request subject with mismatched evaluation template accepted")
	}
	t.Setenv("PPS_EVALUATION_SUBJECT_TEMPLATE", "cbse.other.proj.pps.%s.evaluation")
	if _, err := Load(nil, dbMounts(t)); err != nil {
		t.Fatalf("consistent other-ns subject rejected: %v", err)
	}
}

func TestLoadEvaluationTemplateMismatch(t *testing.T) {
	setEnv(t)
	t.Setenv("PPS_EVALUATION_SUBJECT_TEMPLATE", "cbse.other.proj.pps.%s.evaluation")
	_, err := Load(nil, dbMounts(t))
	if err == nil {
		t.Fatal("evaluation template with mismatched namespace accepted")
	}
	t.Setenv("PPS_EVALUATION_SUBJECT_TEMPLATE", "cbse.default.proj.pps.%s.evaluation")
	if _, err := Load(nil, dbMounts(t)); err != nil {
		t.Fatalf("matching template rejected: %v", err)
	}
}

func TestLoadBadEvaluationTemplate(t *testing.T) {
	setEnv(t)
	for _, s := range []string{
		"cbse.default.proj.pps.evaluation",
		"cbse.default.proj.pps.%s.%s.evaluation",
		"cbse.default.proj.trans.%s.evaluation",
		"cbse.default.proj.pps.42.evaluation",
	} {
		t.Setenv("PPS_EVALUATION_SUBJECT_TEMPLATE", s)
		if _, err := Load(nil, dbMounts(t)); err == nil {
			t.Fatalf("template %q accepted", s)
		}
	}
}

func TestLoadConsumerNameMismatch(t *testing.T) {
	setEnv(t)
	t.Setenv("PPS_CONSUMER", "pps-000000000000")
	if _, err := Load(nil, dbMounts(t)); err == nil {
		t.Fatal("consumer name mismatch accepted")
	}
	t.Setenv("PPS_CONSUMER", "translator-aabbccdd1122")
	if _, err := Load(nil, dbMounts(t)); err == nil {
		t.Fatal("translator consumer name accepted for PPS")
	}
}

func TestLoadBadUID(t *testing.T) {
	setEnv(t)
	t.Setenv("SIMULATIONEXPERIMENTUID", "AABBCCDD-1122-3344-5566-77889900AABB")
	if _, err := Load(nil, dbMounts(t)); err == nil {
		t.Fatal("uppercase UID accepted")
	}
	t.Setenv("SIMULATIONEXPERIMENTUID", "not-a-uid")
	if _, err := Load(nil, dbMounts(t)); err == nil {
		t.Fatal("non-UID accepted")
	}
}

func TestLoadBadStreamOrConsumerName(t *testing.T) {
	setEnv(t)
	t.Setenv("PPS_STREAM", "cbse pps")
	if _, err := Load(nil, dbMounts(t)); err == nil {
		t.Fatal("stream with whitespace accepted")
	}
	setEnv(t)
	t.Setenv("PPS_STREAM", "cbse_pps>1")
	if _, err := Load(nil, dbMounts(t)); err == nil {
		t.Fatal("stream with wildcard accepted")
	}
}

func TestLoadBadResultDBSecret(t *testing.T) {
	setEnv(t)
	cases := []struct {
		name string
		mut  func(dir string)
	}{
		{"missing file", func(dir string) { os.Remove(filepath.Join(dir, "password")) }},
		{"empty value", func(dir string) { os.WriteFile(filepath.Join(dir, "host"), nil, 0o600) }},
		{"bad port", func(dir string) { os.WriteFile(filepath.Join(dir, "port"), []byte("notaport"), 0o600) }},
		{"port out of range", func(dir string) { os.WriteFile(filepath.Join(dir, "port"), []byte("70000"), 0o600) }},
		{"port zero", func(dir string) { os.WriteFile(filepath.Join(dir, "port"), []byte("0"), 0o600) }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			writeDBDir(t, dir, "db.example.com", "5432")
			tc.mut(dir)
			if _, err := Load(nil, Mounts{ResultDBDir: dir}); err == nil {
				t.Fatalf("%s: secret accepted", tc.name)
			}
		})
	}
}

func TestLoadAggregatesErrors(t *testing.T) {
	setEnv(t)
	unsetEnv(t, "NATS_URL")
	t.Setenv("SIMULATIONEXPERIMENTUID", "not-a-uid")
	_, err := Load([]string{"-evaluation-policy", "bogus"}, Mounts{ResultDBDir: ""})
	if err == nil {
		t.Fatal("multiple invalid values accepted")
	}
	for _, want := range []string{"NATS_URL", "SIMULATIONEXPERIMENTUID", "evaluation policy", "result database"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error %v missing %q", err, want)
		}
	}
}
