# Slice S6 — E2E smoke specs: the natural top-up loop, asserted

Normative and self-contained for its worker. Read order and global constraints: [../FEATURE.md](../FEATURE.md) §5 (verbatim discipline), §3 (D4, D5, D6, D7, rulings Q4–Q7), §6 S6 row; then repository-root `AGENTS.md`. S1–S5 + S4R are landed: the live smoke profile runs the real PPS with the fleet-size determinism design — the retained verification runs of 2026-09-29 showed exactly the behavior this spec pins (met-path scenarios: single wave, `Finished`, `runner_round=1`, `computed == number_of_reps`; loop-path scenarios: `1 → +2 → 3 → +11/+26 [cap 30] → met at 14/29`, `runner_round=3`). Your job is to **encode the already-observed truth as a spec** — the system behavior is settled; only the assertion layer is missing. This slice extends, never overrides, the umbrella. All three documents are read-only for you. On any contradiction between this slice and live code that inspection cannot resolve: stop and ask via your ask channel.

## Target

Exactly one file: `test/e2e/smoke_test.go`.

Everything else is untouchable — in particular the other four specs in that file stay byte-identical (`reaches InProgress…`, `persists one project…`, `drives one scenario…Finished…`, `reconciles an idempotent metadata update…`, `garbage-collects…`), `test/harness/**`, `test/e2e/manifests/**`, `test/mocks/**`, all modules, root `Makefile`, `docs/**`, `devlog/**`.

## Change

Add **one new Ordered spec**, placed between the chain spec (`drives one scenario … to Finished …`, ends ~line 341) and the idempotence spec (~line 343), titled `"converges all four scenarios: met-path single-wave bookkeeping and loop-path natural top-up"`. Structure it exactly like the existing specs (Ginkgo `It`, the `queryDatabase`/`writeDatabaseArtifact` helpers, the existing k8s Job-listing pattern from the chain spec):

1. **Convergence gate** — `Eventually` (8 min, 5 s): `SELECT COUNT(*) FROM scenario_status ss JOIN project p ON p.id=ss.project_id WHERE p.project_name='<project>' AND ss.state='Finished'` → `"4"`. (The chain spec has already proven one met-path `Finished`; this gate waits for all four — the loops converge within ~1–2 min of the met scenarios in the observed runs.)
2. **Per-family bookkeeping** — one query per family, parsed like the chain spec parses rows:
   - Met-path (both `priority=1` rows): `runner_round == 1`, `round_reps == 40` (`number_of_reps`), `number_of_computed_reps == 40`, `evaluation_attempts >= 1`.
   - Loop-path (both `priority=2` rows): `runner_round >= 2` (observed 3; do NOT pin equality — wave counts vary with the estimator), `number_of_reps == 1`, `number_of_computed_reps > 1` (observed 14/29), `round_reps <= 30` (the wave cap from the experiment args — ruling Q4/Q7's pacing made observable), `evaluation_attempts >= runner_round` (each round published once; `>=` tolerates the at-least-once redelivery the settlement runs observed).
   - Echo the full four-row dump (id, state, priority, number_of_reps, runner_round, round_reps, number_of_computed_reps, evaluation_attempts, confidence_metric) via `writeDatabaseArtifact` for diagnostics.
3. **Round-Job identity asserts** — list Jobs by the project label (the chain spec's pattern):
   - Exactly **two** Jobs carry the round label `experiment.cbse.terministic.de/runner-round: "2"` (one per loop scenario — each round is one Job), and each such Job's name contains `"-r2"` (S1's name suffix) and its scenario-id label matches one of the two loop scenarios.
   - Jobs with the runner-round label `"3"`: `>= 0` (may or may not exist depending on convergence; do not pin).
   - **No Job of a met-path scenario carries a round label > "1"** — for each priority-1 scenario, every listed Job has runner-round `"1"` and its name has no `-r` suffix (the byte-identical single-round format).

## Constraints

- **Surgical**: one new `It` + nothing else; the other specs byte-identical; no helper changes unless a trivial private one is unavoidable (report it if so).
- Assert **structural invariants, not observed coincidences**: never pin exact wave counts beyond the guaranteed `>= 2`, never pin exact computed totals beyond `> 1` / `== 40`, never pin evaluation_attempts equality (redelivery tolerance).
- The spec must hold in both smoke modes: the normal run and the retained mode (`CBSE_RETAIN_RESOURCES=1` — the GC spec skips itself there; your spec always runs).
- Comments in the spec explain the two families and cite the design (fleet-size determinism, ruling Q6; the cap, Q4/Q7).
- `make test-fast` rc=0 is mandatory (the e2e compile lines run inside it). **No cluster operations, no `make test-smoke`** — the settlement smoke (which also re-verifies the S4R image) is the manager's gate after your `worker_done`.
- Global constraints of FEATURE.md §5 apply verbatim (attestation, no commits, read-only specs, scope discipline, verification honesty).

## Ownership

Sole owner of `test/e2e/smoke_test.go` in this solo wave. Discovered gaps → report lines, do not fix.

## Observable acceptance

Run and echo all of these in `worker_done` (the manager re-runs each, then runs the settlement smoke):

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat; mismatch → stop, `--outcome failed`).
2. Containment: `git status --short` shows only `test/e2e/smoke_test.go`.
3. `make test-fast` rc=0 final receipt (mandatory — includes the e2e compile/vet over your spec).
4. `cd test/e2e && go vet -tags=e2e ./... && go test -tags=e2e -run '^$' ./...` (compile-only) → rc=0.
5. The new spec's assert inventory: echo the final list of asserts (families, invariants, Job identity rules) with the line numbers.
6. Byte-identity proof: `git diff --stat` shows only smoke_test.go; and the diff hunk count for the other specs is zero (echo `git diff -U0 test/e2e/smoke_test.go | grep -c '^[+-]'` plus a sentence confirming the hunks fall only between the chain spec's end and the idempotence spec).

Completion protocol: `worker_done` with a three-sentence executive summary, both lifecycle IDs (task + dispatch from your preamble), explicit `--outcome succeeded|failed`, the verbatim attestation line, the six evidence blocks, `--files-modified`.

**Session hygiene (binding):** keep tool outputs small; gather evidence incrementally; never batch everything into one giant end-session run.
