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

package generator

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// canonicalModelDir is the canonical model source, resolved module-relative
// from this package directory (pads-paper/translator/component/internal/
// generator -> ../../../../simulation-model/src). Go tests run with the
// package directory as the working directory, so the path resolves no matter
// where `go test` is invoked from.
const canonicalModelDir = "../../../../simulation-model/src"

// canonicalRel is the repository-relative canonical source named in failure
// messages as the source of truth.
const canonicalRel = "pads-paper/simulation-model/src"

// syncedRel is the repository-relative path of the synchronized runnermod
// copies, used in the restore-parity copy command.
const syncedRel = "pads-paper/translator/component/internal/generator/runnermod"

// canonicalRuledFiles are exactly the six synchronized production model
// files. The standalone-CLI-only cli.py and test_cli.py are not part of the
// synchronized surface and never enter runnermod.
var canonicalRuledFiles = []string{
	"model.py",
	"result.py",
	"seed.py",
	"test_model.py",
	"test_result.py",
	"test_seed.py",
}

// TestCanonicalParity fails go test whenever a synchronized runnermod copy
// drifts from the canonical model source. The embedded bytes (what the
// generator ships into every generated runner) must be byte-identical to the
// canonical file; the paper-local Translator never edits the copies by hand,
// drift is resolved by re-copying from canonical, and the canonical source
// stays the single source of truth. The test fails (never skips) when the
// canonical directory or any ruled file is missing on either side, and it
// mutates no file.
func TestCanonicalParity(t *testing.T) {
	info, err := os.Stat(canonicalModelDir)
	if err != nil || !info.IsDir() {
		t.Fatalf(
			"canonical model source missing at %s (%v): %s is the source of truth and is required; re-copy the six ruled files from there",
			canonicalModelDir, err, canonicalRel,
		)
	}
	for _, name := range canonicalRuledFiles {
		t.Run(name, func(t *testing.T) {
			canonical, err := os.ReadFile(filepath.Join(canonicalModelDir, name))
			if err != nil {
				t.Fatalf(
					"canonical file missing: %s/%s is the source of truth for runnermod/%s and is required; restore parity with: cp %s/%s %s/%s",
					canonicalRel, name, name, canonicalRel, name, syncedRel, name,
				)
			}
			embedded, err := runnerFS.ReadFile("runnermod/" + name)
			if err != nil {
				t.Fatalf(
					"synchronized copy missing from the embed: runnermod/%s (source of truth: %s/%s); restore parity with: cp %s/%s %s/%s",
					name, canonicalRel, name, canonicalRel, name, syncedRel, name,
				)
			}
			if !bytes.Equal(canonical, embedded) {
				t.Fatalf(
					"drift: runnermod/%s differs from canonical %s/%s (source of truth); restore parity with: cp %s/%s %s/%s",
					name, canonicalRel, name, canonicalRel, name, syncedRel, name,
				)
			}
		})
	}
}
