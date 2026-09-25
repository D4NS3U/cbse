# Dispatch prompt — E2: translator race-tier test-timeout bump (cold-stall hardening)

**Task title:** E2 — Raise the translator race-suite per-binary test timeout to 30 minutes (M0 hardening note)

**Read order (mandatory, normative, read-only):** 1) repository-root `AGENTS.md` — the test contract; note this change is a **test-harness entry-point change**, so `make test-fast` is mandatory evidence; 2) this task spec. On contradiction: stop and ask.

**Context you may rely on:** the M0 external fresh-clone review recorded an environmental failure class: on a cold-cache host, `component-templates/translator && go test -race ./...` had 11 of 12 race-instrumented test binaries killed at Go's default per-binary test timeout (10 minutes) under cold-compile + parallel race-instrumentation load; every package passes warm and in isolation (`rc=0`, 60 `ok`). The mandated hardening raises only that ceiling.

## Target (your ownership)

Exactly two files: the repository-root `Makefile` (one invocation line) and `CHANGELOG.md` (one bullet). Untouchable: everything else — no Go sources, no other Makefile lines, no harness scripts.

## Change

1. In the root `Makefile`'s `test-fast` recipe, change exactly the translator race line:
   - from: `cd component-templates/translator && go test -race ./...`
   - to: `cd component-templates/translator && go test -race -timeout 30m ./...`
   No other Makefile byte changes (the scenario-manager race line stays as-is per mandate).
2. Add one bullet to `CHANGELOG.md` under `## [Unreleased]` → `### Changed` (top of that section, matching the file's formatting style): a line stating that the Translator module's race suite now runs with a 30-minute per-binary test timeout because cold-cache hosts could exhaust Go's 10-minute default under race instrumentation, killing healthy test binaries (M0 review finding).

## Constraints

- Test integrity (P1): this raises a timeout ceiling only; no test, tier, assertion, or default is weakened, removed, or skipped; the scenario-manager race line and every other target byte-identical.
- `make test-fast` must pass rc=0 after your change (mandatory evidence — Makefile change under the AGENTS.md tier contract). It runs the full suite including the changed translator tier.
- No cluster operations, no network beyond the suite's own public downloads, no commits (P6 — working tree only). Scope discipline: nothing beyond the two named edits.

## Observable acceptance (all echoed in your `worker_done`; the manager re-runs each)

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat in your executive summary; mismatch → stop, change nothing, `--outcome failed`).
2. Diff discipline: `git diff Makefile` shows exactly one changed line (`-timeout 30m` added to the translator race invocation; zero other hunks); `git diff CHANGELOG.md` shows exactly one added bullet.
3. Grep proof: `grep -n "timeout 30m" Makefile` shows the line; `grep -c "go test -race" Makefile` unchanged (2); `grep -n "go test -race ./..." Makefile` shows the scenario-manager line still default-timeout.
4. Tier proof: `make test-fast` rc=0 — echo the command's final rc line.
5. Containment: `git status --short` shows exactly ` M Makefile` and ` M CHANGELOG.md`.
6. Completion protocol: three-sentence executive summary; both lifecycle IDs (task + dispatch); explicit `--outcome succeeded|failed`; the verbatim attestation line.
