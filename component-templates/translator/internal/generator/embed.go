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
