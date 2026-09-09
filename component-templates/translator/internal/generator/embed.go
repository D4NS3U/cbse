// embed.go ships the runner runtime modules (the SimPy model, result sink,
// entrypoint, and the database_endpoint contract package) as embedded files
// the generator copies into every generated runner. The modules contain no
// credentials and are shipped code, not separately deployed services.
package generator

import (
	"embed"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

//go:embed all:runnermod
var runnerFS embed.FS

// writeEmbeddedRunner copies the embedded runner modules into runnerDir,
// preserving the directory layout. Test files (test_*) and __pycache__ entries
// are skipped so they never ship in a generated runner image.
func writeEmbeddedRunner(runnerDir string) error {
	return fs.WalkDir(runnerFS, "runnermod", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		base := filepath.Base(path)
		if strings.HasPrefix(base, "test_") || strings.Contains(path, "__pycache__") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		rel := strings.TrimPrefix(path, "runnermod/")
		dest := filepath.Join(runnerDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return fmt.Errorf("generator: create %s: %w", filepath.Dir(dest), err)
		}
		data, err := runnerFS.ReadFile(path)
		if err != nil {
			return fmt.Errorf("generator: read embedded %s: %w", path, err)
		}
		mode := os.FileMode(0o644)
		if base == "resultdb.json" {
			mode = 0o600
		}
		if err := os.WriteFile(dest, data, mode); err != nil {
			return fmt.Errorf("generator: write %s: %w", dest, err)
		}
		return nil
	})
}
