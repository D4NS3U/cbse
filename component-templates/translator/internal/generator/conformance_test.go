package generator

import (
	"bytes"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPythonRunnerConformance runs the embedded Python runner test suites:
//
//   - database_endpoint.test_dispatcher: the S05-A3 cross-client endpoint
//     conformance suite (the generated runner leg).
//   - test_seed, test_result, test_resultdb: pure-logic runner unit tests
//     (seed derivation, result-record fields, Result DB SQL/transaction
//     structure) that run without simpy or psycopg.
//   - test_model: the SimPy queue-horizon, no-completion, and PRNG tests, which
//     skip when simpy is not installed.
//
// The suites run against the shipped modules written from the embed.FS, not the
// source tree. The whole test is skipped when python3 is unavailable.
func TestPythonRunnerConformance(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available; skipping Python runner conformance")
	}
	// Guard: the shipped package must declare the 10-second shared deadline.
	data, err := runnerFS.ReadFile("runnermod/database_endpoint/__init__.py")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "RESOLUTION_DEADLINE_SECONDS = 10.0") {
		t.Fatalf("embedded database_endpoint/__init__.py must declare RESOLUTION_DEADLINE_SECONDS = 10.0")
	}

	dir := t.TempDir()
	// Write the entire shipped runnermod (modules + test files), excluding
	// __pycache__, so the suites run against the shipped code.
	if err := writeRunnermod(t, dir); err != nil {
		t.Fatal(err)
	}

	// 1. Dispatcher conformance (relative imports -> run as a module).
	if out, err := runPython(t, dir, "python3", "-m", "database_endpoint.test_dispatcher"); err != nil {
		t.Fatalf("dispatcher conformance failed: %v\n%s", err, out)
	} else if !strings.Contains(out, "OK") {
		t.Fatalf("dispatcher conformance did not report OK:\n%s", out)
	}

	// 2. Pure-logic + SimPy runner unit tests. test_model skips without simpy.
	if out, err := runPython(t, dir, "python3", "-m", "unittest",
		"test_seed", "test_result", "test_resultdb", "test_model"); err != nil {
		t.Fatalf("runner unit tests failed: %v\n%s", err, out)
	} else if !strings.Contains(out, "OK") {
		t.Fatalf("runner unit tests did not report OK:\n%s", out)
	}
}

func writeRunnermod(t *testing.T, dir string) error {
	return fs.WalkDir(runnerFS, "runnermod", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if strings.Contains(path, "__pycache__") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, "runnermod/")
		dest := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		b, err := runnerFS.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, b, 0o644)
	})
}

func runPython(t *testing.T, dir string, name string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}
