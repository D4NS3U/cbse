package config

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func writeDBDir(t *testing.T, dir string, host string, port string) {
	t.Helper()
	for k, v := range map[string]string{
		"host": host, "port": port, "dbname": "db", "user": "u", "password": "p",
	} {
		if err := os.WriteFile(filepath.Join(dir, k), []byte(v), 0o600); err != nil {
			t.Fatalf("write %s: %v", k, err)
		}
	}
}

func writeRegistryConfig(t *testing.T, path, authority string) {
	t.Helper()
	auth := base64.StdEncoding.EncodeToString([]byte("robot:token"))
	cfg := `{"auths":{"` + authority + `":{"auth":"` + auth + `"}}}`
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write registry config: %v", err)
	}
}

func setEnv(t *testing.T) {
	t.Helper()
	sets := map[string]string{
		"NATS_URL":                          "nats://127.0.0.1:4222",
		"TRANSLATOR_STREAM":                 "cbse-experiment",
		"TRANSLATOR_REQUEST_SUBJECT":        "cbse.default.proj.trans.request",
		"TRANSLATOR_READY_SUBJECT_TEMPLATE": "cbse.{namespace}.{project}.trans.{scenario_id}.ready",
		"TRANSLATOR_CONSUMER":               "translator-aabbccdd1122",
		"SIMULATIONPROJECTNAMESPACE":        "default",
		"SIMULATIONPROJECTNAME":             "proj",
		"SIMULATIONEXPERIMENTUID":           "aabbccdd-1122-3344-5566-77889900aabb",
		"REPOSITORY":                        "registry.example.com/proj/runner",
		"BASEIMAGE":                         "registry.example.com/proj/runner-base@sha256:" + rep("a", 64),
	}
	for k, v := range sets {
		t.Setenv(k, v)
	}
}

func rep(s string, n int) string {
	out := ""
	for i := 0; i < n; i++ {
		out += s
	}
	return out
}

func TestLoadValid(t *testing.T) {
	setEnv(t)
	dir := t.TempDir()
	detail := filepath.Join(dir, "detail")
	result := filepath.Join(dir, "result")
	os.MkdirAll(detail, 0o755)
	os.MkdirAll(result, 0o755)
	writeDBDir(t, detail, "db.example.com", "5432")
	writeDBDir(t, result, "10.0.0.1", "5433")
	reg := filepath.Join(dir, "config.json")
	writeRegistryConfig(t, reg, "registry.example.com")
	cfg, err := Load(Mounts{DetailDBDir: detail, ResultDBDir: result, RegistryAuth: reg})
	if err != nil {
		t.Fatalf("Load err = %v", err)
	}
	if cfg.Namespace != "default" || cfg.Project != "proj" {
		t.Fatalf("ns/proj = %q/%q", cfg.Namespace, cfg.Project)
	}
	if cfg.UIDPrefix12() != "aabbccdd1122" {
		t.Fatalf("UIDPrefix12 = %q, want aabbccdd1122", cfg.UIDPrefix12())
	}
	if cfg.DetailDB.Port != 5432 || cfg.ResultDB.Port != 5433 {
		t.Fatalf("db ports = %d/%d", cfg.DetailDB.Port, cfg.ResultDB.Port)
	}
}

func TestLoadRejectsNATSUserInfo(t *testing.T) {
	setEnv(t)
	t.Setenv("NATS_URL", "nats://user:pass@127.0.0.1:4222")
	if _, err := Load(Mounts{}); err == nil {
		t.Fatal("NATS_URL with userinfo must be rejected")
	}
}

func TestLoadRejectsNamespaceMismatch(t *testing.T) {
	setEnv(t)
	t.Setenv("SIMULATIONPROJECTNAMESPACE", "other")
	if _, err := Load(Mounts{}); err == nil {
		t.Fatal("namespace mismatch must be rejected")
	}
}

func TestLoadRejectsNonDigestBaseImage(t *testing.T) {
	setEnv(t)
	t.Setenv("BASEIMAGE", "registry.example.com/proj/runner-base:latest")
	if _, err := Load(Mounts{}); err == nil {
		t.Fatal("non-digest base image must be rejected")
	}
}

func TestLoadRejectsTaggedRepository(t *testing.T) {
	setEnv(t)
	t.Setenv("REPOSITORY", "registry.example.com/proj/runner:latest")
	if _, err := Load(Mounts{}); err == nil {
		t.Fatal("tagged repository must be rejected")
	}
}

func TestLoadRejectsMissingDBFiles(t *testing.T) {
	setEnv(t)
	dir := t.TempDir()
	if _, err := Load(Mounts{DetailDBDir: filepath.Join(dir, "missing"), ResultDBDir: filepath.Join(dir, "missing"), RegistryAuth: filepath.Join(dir, "missing.json")}); err == nil {
		t.Fatal("missing DB files must be rejected")
	}
}

func TestLoadRejectsMissingRegistryAuth(t *testing.T) {
	setEnv(t)
	dir := t.TempDir()
	detail := filepath.Join(dir, "detail")
	result := filepath.Join(dir, "result")
	os.MkdirAll(detail, 0o755)
	os.MkdirAll(result, 0o755)
	writeDBDir(t, detail, "db.example.com", "5432")
	writeDBDir(t, result, "10.0.0.1", "5433")
	if _, err := Load(Mounts{DetailDBDir: detail, ResultDBDir: result, RegistryAuth: filepath.Join(dir, "nope.json")}); err == nil {
		t.Fatal("missing registry auth must be rejected")
	}
}

func TestLoadRejectsRegistryWithoutCredentials(t *testing.T) {
	setEnv(t)
	dir := t.TempDir()
	detail := filepath.Join(dir, "detail")
	result := filepath.Join(dir, "result")
	os.MkdirAll(detail, 0o755)
	os.MkdirAll(result, 0o755)
	writeDBDir(t, detail, "db.example.com", "5432")
	writeDBDir(t, result, "10.0.0.1", "5433")
	reg := filepath.Join(dir, "config.json")
	// Credentials for a different authority: resolution for the configured
	// authority must fail.
	writeRegistryConfig(t, reg, "other.example.com")
	if _, err := Load(Mounts{DetailDBDir: detail, ResultDBDir: result, RegistryAuth: reg}); err == nil {
		t.Fatal("missing credentials for the configured authority must be rejected")
	}
}

func TestLoadRejectsMissingRequiredEnv(t *testing.T) {
	// Clear all CBSE env and assert a multi-error mentioning each required var.
	for _, k := range []string{"NATS_URL", "TRANSLATOR_STREAM", "TRANSLATOR_REQUEST_SUBJECT", "TRANSLATOR_READY_SUBJECT_TEMPLATE", "TRANSLATOR_CONSUMER", "SIMULATIONPROJECTNAMESPACE", "SIMULATIONPROJECTNAME", "SIMULATIONEXPERIMENTUID", "REPOSITORY", "BASEIMAGE"} {
		t.Setenv(k, "")
	}
	_, err := Load(Mounts{})
	if err == nil {
		t.Fatal("empty configuration must be rejected")
	}
	for _, want := range []string{"NATS_URL", "TRANSLATOR_STREAM", "TRANSLATOR_REQUEST_SUBJECT", "TRANSLATOR_READY_SUBJECT_TEMPLATE", "TRANSLATOR_CONSUMER", "SIMULATIONPROJECTNAMESPACE", "SIMULATIONPROJECTNAME", "SIMULATIONEXPERIMENTUID", "REPOSITORY", "BASEIMAGE"} {
		if !contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}

func TestLoadRejectsConsumerNotUIDSpecific(t *testing.T) {
	setEnv(t)
	t.Setenv("TRANSLATOR_CONSUMER", "translator-deadbeef0000") // wrong UID prefix
	dir := t.TempDir()
	detail := filepath.Join(dir, "detail")
	result := filepath.Join(dir, "result")
	os.MkdirAll(detail, 0o755)
	os.MkdirAll(result, 0o755)
	writeDBDir(t, detail, "db.example.com", "5432")
	writeDBDir(t, result, "10.0.0.1", "5433")
	reg := filepath.Join(dir, "config.json")
	writeRegistryConfig(t, reg, "registry.example.com")
	_, err := Load(Mounts{DetailDBDir: detail, ResultDBDir: result, RegistryAuth: reg})
	if err == nil {
		t.Fatal("consumer not matching translator-<UIDPrefix> must be rejected")
	}
	if !contains(err.Error(), "UID-specific durable name") {
		t.Fatalf("error %q should mention UID-specific durable name", err.Error())
	}
}

func contains(hay, needle string) bool {
	for i := 0; i+len(needle) <= len(hay); i++ {
		if hay[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
