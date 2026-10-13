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
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeSecret writes the five Detail DB connection files into a temp dir.
// keys maps file name to content; a missing key omits the file, an empty
// value writes an empty file.
func writeSecret(t *testing.T, keys map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for k, v := range keys {
		if err := os.WriteFile(filepath.Join(dir, k), []byte(v), 0o600); err != nil {
			t.Fatalf("write %s: %v", k, err)
		}
	}
	return dir
}

func fullSecret() map[string]string {
	return map[string]string{"host": "detaildb", "port": "5432", "dbname": "simulation_db", "user": "eds", "password": "s3cret"}
}

// validEnv sets the full valid environment for the EDS.
func validEnv(t *testing.T) {
	t.Helper()
	t.Setenv("NATS_URL", "nats://sm-eds-nats:4222")
	t.Setenv("EDS_AVAILABILITY_SUBJECT", "cbse.ns.exp.eds.scenarios.available")
	t.Setenv("SIMULATIONPROJECTNAMESPACE", "ns")
	t.Setenv("SIMULATIONPROJECTNAME", "exp")
	t.Setenv("SIMULATIONEXPERIMENTUID", "aabbccdd-1122-3344-5566-77889900aabb")
}

// TestLoadValid is the happy path: every field validates and the canonical
// subject matches the identity-derived subject.
func TestLoadValid(t *testing.T) {
	validEnv(t)
	dir := writeSecret(t, fullSecret())
	cfg, err := Load([]string{}, Mounts{DetailDBDir: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.NATSURL != "nats://sm-eds-nats:4222" || cfg.AvailabilitySubject != "cbse.ns.exp.eds.scenarios.available" {
		t.Fatalf("subjects = %q / %q", cfg.NATSURL, cfg.AvailabilitySubject)
	}
	if cfg.Namespace != "ns" || cfg.Project != "exp" || cfg.ExperimentUID != "aabbccdd-1122-3344-5566-77889900aabb" {
		t.Fatalf("identity = %+v", cfg)
	}
	if cfg.DetailDB.Host != "detaildb" || cfg.DetailDB.Port != 5432 || cfg.DetailDB.DBName != "simulation_db" || cfg.DetailDB.User != "eds" || cfg.DetailDB.Password != "s3cret" {
		t.Fatalf("detail db = %+v", cfg.DetailDB)
	}
	if cfg.Listen != ":8082" {
		t.Fatalf("default listen = %q", cfg.Listen)
	}
}

// TestLoadListenFlag covers the container-arg listen address.
func TestLoadListenFlag(t *testing.T) {
	validEnv(t)
	dir := writeSecret(t, fullSecret())
	cfg, err := Load([]string{"-listen", ":9090"}, Mounts{DetailDBDir: dir})
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Listen != ":9090" {
		t.Fatalf("listen = %q", cfg.Listen)
	}
	if _, err := Load([]string{"-unknown-flag"}, Mounts{DetailDBDir: dir}); err == nil {
		t.Fatalf("unknown flag must fail")
	}
}

// TestLoadMissingEnv covers every required environment variable.
func TestLoadMissingEnv(t *testing.T) {
	validEnv(t)
	dir := writeSecret(t, fullSecret())
	for _, key := range []string{"NATS_URL", "EDS_AVAILABILITY_SUBJECT", "SIMULATIONPROJECTNAMESPACE", "SIMULATIONPROJECTNAME", "SIMULATIONEXPERIMENTUID"} {
		t.Setenv(key, "")
		_, err := Load([]string{}, Mounts{DetailDBDir: dir})
		if err == nil {
			t.Fatalf("missing %s must fail", key)
		}
		if !strings.Contains(err.Error(), key) {
			t.Fatalf("error for missing %s must name the field: %v", key, err)
		}
		validEnv(t)
	}
}

// TestLoadMalformedEnv covers malformed values that name the exact field.
func TestLoadMalformedEnv(t *testing.T) {
	validEnv(t)
	dir := writeSecret(t, fullSecret())
	t.Setenv("NATS_URL", "nats://user:pass@sm-eds-nats:4222")
	if _, err := Load([]string{}, Mounts{DetailDBDir: dir}); err == nil || !strings.Contains(err.Error(), "NATS_URL") {
		t.Fatalf("userinfo URL must fail naming NATS_URL: %v", err)
	}
	t.Setenv("NATS_URL", "nats://sm-eds-nats:4222")
	t.Setenv("SIMULATIONPROJECTNAMESPACE", "SIM_BAD")
	if _, err := Load([]string{}, Mounts{DetailDBDir: dir}); err == nil || !strings.Contains(err.Error(), "SIMULATIONPROJECTNAMESPACE") {
		t.Fatalf("bad namespace must fail naming the field: %v", err)
	}
	t.Setenv("SIMULATIONPROJECTNAMESPACE", "ns")
	t.Setenv("SIMULATIONEXPERIMENTUID", "not-a-uid")
	if _, err := Load([]string{}, Mounts{DetailDBDir: dir}); err == nil || !strings.Contains(err.Error(), "SIMULATIONEXPERIMENTUID") {
		t.Fatalf("bad UID must fail naming the field: %v", err)
	}
}

// TestLoadAvailabilitySubjectMismatch covers the exact-match rule: the
// subject must equal cbse.<ns>.<proj>.eds.scenarios.available.
func TestLoadAvailabilitySubjectMismatch(t *testing.T) {
	validEnv(t)
	dir := writeSecret(t, fullSecret())
	for _, subject := range []string{
		"cbse.other.exp.eds.scenarios.available",
		"cbse.ns.other.eds.scenarios.available",
		"cbse.ns.exp.eds.scenarios",
		"cbse.ns.exp.eds.scenarios.available.extra",
	} {
		t.Setenv("EDS_AVAILABILITY_SUBJECT", subject)
		_, err := Load([]string{}, Mounts{DetailDBDir: dir})
		if err == nil {
			t.Fatalf("subject %q must be refused", subject)
		}
		if !strings.Contains(err.Error(), "EDS_AVAILABILITY_SUBJECT") {
			t.Fatalf("error must name the field: %v", err)
		}
	}
}

// TestLoadSecretMissingField covers every Secret key missing or empty.
func TestLoadSecretMissingField(t *testing.T) {
	for _, key := range []string{"host", "port", "dbname", "user", "password"} {
		t.Run(key+" missing", func(t *testing.T) {
			keys := fullSecret()
			delete(keys, key)
			dir := writeSecret(t, keys)
			if _, err := LoadSecret(dir); err == nil {
				t.Fatalf("missing %s must fail", key)
			}
		})
		t.Run(key+" empty", func(t *testing.T) {
			keys := fullSecret()
			keys[key] = "   "
			dir := writeSecret(t, keys)
			if _, err := LoadSecret(dir); err == nil {
				t.Fatalf("empty %s must fail", key)
			}
		})
	}
}

// TestLoadSecretBadPort covers the port domain and non-integer ports.
func TestLoadSecretBadPort(t *testing.T) {
	for _, port := range []string{"0", "65536", "-1", "abc"} {
		keys := fullSecret()
		keys["port"] = port
		dir := writeSecret(t, keys)
		if _, err := LoadSecret(dir); err == nil {
			t.Fatalf("port %q must fail", port)
		}
	}
}

// TestLoadSecretEmptyDir covers an empty mount path.
func TestLoadSecretEmptyDir(t *testing.T) {
	if _, err := LoadSecret(""); err == nil {
		t.Fatalf("empty dir must fail")
	}
	if _, err := LoadSecret(t.TempDir() + "/nonexistent"); err == nil {
		t.Fatalf("missing dir must fail")
	}
}

// TestValidate covers the DatabaseConfig Validate field checks.
func TestValidate(t *testing.T) {
	valid := DatabaseConfig{Host: "h", Port: 5432, DBName: "d", User: "u", Password: "p"}
	if err := valid.Validate(); err != nil {
		t.Fatalf("valid config fails: %v", err)
	}
	cases := map[string]DatabaseConfig{
		"no host":     {Host: "", Port: 5432, DBName: "d", User: "u", Password: "p"},
		"no port":     {Host: "h", Port: 0, DBName: "d", User: "u", Password: "p"},
		"port high":   {Host: "h", Port: 65536, DBName: "d", User: "u", Password: "p"},
		"no dbname":   {Host: "h", Port: 5432, DBName: "", User: "u", Password: "p"},
		"no user":     {Host: "h", Port: 5432, DBName: "d", User: "", Password: "p"},
		"no password": {Host: "h", Port: 5432, DBName: "d", User: "u", Password: ""},
	}
	for name, c := range cases {
		if err := c.Validate(); err == nil {
			t.Fatalf("%s must fail", name)
		}
	}
}
