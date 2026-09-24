# Dispatch prompt — L1: Apache-2.0 license-header pass + generator boilerplate + CDG licensing section

**Task title:** L1 — Uniform Apache-2.0 license headers across eligible source files; canonical generator boilerplate; COMPONENT_DESIGN_GOALS licensing section (user decisions 2026-09-24)

**Read order (mandatory, all normative and read-only for you):** 1) `devlog/changes/public-repo-hygiene/FEATURE.md` §5 global constraints and §7 recipes; 2) repository-root `AGENTS.md` (current, rewritten public-tone text — normative for your run); 3) the source files themselves. On any contradiction: stop and ask via your ask channel.

**User decisions you implement (recorded 2026-09-24):** give license headers to the Go files and every other file class that requires one (uniform canonical Apache-2.0 short-form header with holder `Daniel Seufferth` — from the repository LICENSE's own copyright line), and document the component-licensing expectation in `docs/COMPONENT_DESIGN_GOALS.md`. The copyright line form is **`Copyright 2025-2026 Daniel Seufferth`** (plain ASCII hyphen).

## Target (your ownership, disjoint from the parallel S2 worker)

- All tracked, worktree-present `*.go` files in `experiment-operator/`, `scenario-manager/`, `component-templates/`, `test/` — both the ~133 lacking a header and the ~16 carrying the stock `/* Copyright 2025. … */` block (normalize).
- `experiment-operator/hack/boilerplate.go.txt` — the generator template feeding `zz_generated*.go` headers.
- All tracked, worktree-present `*.sh` (12 live: `test/harness/` ×11 + `experiment-operator/.devcontainer/post-install.sh`) and `*.py` (17 live: `component-templates/translator/internal/generator/runnermod/**`, `test/mocks/**`).
- `docs/COMPONENT_DESIGN_GOALS.md` — add one licensing section.

**Untouchable:** `LICENSE` (P3: verify-and-report object only — your run must leave it byte-identical), `README.md` and `SECURITY.md` (S2's parallel partition), every spec/charter/`MANAGER.md`, `test/harness` logic (R4: comments may be added at file top; no script or test logic changes), and `test/compat/eds-sm/**` (deleted already; deleted-but-still-indexed paths are not in the worktree and are not yours).

## Change

1. **Canonical header (Go, `//` line form — use exactly this text, inserting as the file's leading lines, before any package clause/build constraint except existing build tags are kept directly under the header):**
   ```
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
   ```
   `*.sh`: the same text as `#` comment lines, placed **after the shebang line** (`#!/usr/bin/env bash` stays line 1; the harness's `set -euo pipefail` and everything else stay byte-identical below the inserted header). `*.py`: same text as `#` comment lines at the very top (before module docstrings — do not alter docstrings or code).
2. **Generator consistency (required — this is the mechanism, not a hand-edit):** rewrite `experiment-operator/hack/boilerplate.go.txt` to the canonical `//` header text above, then run `make -C experiment-operator generate` so every `zz_generated*.go` re-derives with the canonical header. Never hand-edit generated files; regeneration plus `make test-fast` (which runs `verify-generated`) proves consistency.
3. **CDG section (one additive section, nothing else in that file):** add a concise section (e.g., "Licensing your components") stating that contributed component sources (custom EDS, Translator, PostProcessing images and their source files) carry the same Apache-2.0 short-form header (`Copyright <years> <holder>`), recommending `SPDX-License-Identifier: Apache-2.0`, and showing the header form as a fenced example. Do not refresh other CDG content (its alpha3-era framing is a separate, later slice; only this section is licensed).

## Constraints (binding summary; umbrella §5 carries P1–P8)

- **Header-only discipline:** every hunk in every `*.go`/`*.sh`/`*.py` you touch is the license-header insertion and — for the ~16 stock-header files — the replacement of the old `/* Copyright 2025. … */` block with the canonical form. Zero logic/blank-significant changes; `LICENSE`, tests' assertions, harness behavior all unchanged.
- P1: never weaken a test; failures are reported, not worked around. No cluster operations; no commits (P6); secrets hygiene per umbrella §5.
- Scope discipline: nothing beyond your named ownership; discovered gaps (e.g., a file class you believe needs headers but is not listed) → report line, never a silent expansion.

## Observable acceptance (all echoed in your worker_done; the manager re-runs each)

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat in your executive summary; mismatch → stop, change nothing, `--outcome failed`).
2. Completeness: for each class, `git ls-files -- '*.go'` / `'*.sh'` / `'*.py'` (minus worktree-absent paths) | xargs grep -L -F "Copyright 2025-2026 Daniel Seufferth" → empty list each.
3. Normalization: `grep -rn "Copyright 2025\."` over tracked `*.go` → rc=1 (the canonical form contains none with bare dot); old `/*` boilerplate gone from `hack/boilerplate.go.txt`.
4. Generation idempotence: a second `make -C experiment-operator generate` run produces a zero-byte diff; `License` untouched: empty `git status --short -- LICENSE`.
5. Diff discipline: `git diff --stat` shows every touched source file with changes exclusively in insertions/removals of header lines; echo the stat summary and a per-class hunk-count audit (your own grep over `git diff -U0` excluding header-only vocabulary + the CDG hunk must be empty of surprises).
6. **`make test-fast` rc=0 recorded** (mandatory per the current `AGENTS.md`: mass Go-source changes; includes `verify-generated`, the harness self-tests, race suites, the operator envtest, and the translator's Python runner conformance suite). Echo the trailing rc line; any FAIL → `--outcome failed` with exact output.
7. CDG proof: `git diff docs/COMPONENT_DESIGN_GOALS.md` shows exactly one additive section hunk, and the R3 link-check recipe (umbrella §7) still passes with no BROKEN lines.
