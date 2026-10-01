# T3 — The F-crash Failed-chain spec: the misdirected result sink → the live Failed chain

**Dispatch record (Wave 3, experiment-terminal-e2e).** Read order: `devlog/changes/experiment-terminal-e2e/FEATURE.md` (§3 D4, D5; §5) → `AGENTS.md` → this spec → code. On contradiction the FEATURE.md cannot resolve through repository inspection: stop and ask via your `ask` channel.

## Target

- `test/e2e/smoke_test.go` — exactly one new `It(...)` after the flavor-3 spec (`<project>-errready`) and before the GC spec, plus any small unexported helpers local to the file.

## Change

One red experiment proves the live **Failed chain** (ruling F: the single route; ruling S: the mechanism is not the spec's subject — the refined trigger needs **zero component changes**):

1. **The red CR** (`<project>-fcrash`), built by deep-copying the live green experiment's spec with exactly two deltas:
   - the name;
   - `resultDatabase` switched to the **host-based form**: `host` = `<red>-detaildb-svc`, `port` = the detail database's port (the value the spec already declares in `detailDatabase.port`), `dbname` = the detail database's dbname, `user`/`password` = the same values the spec declares for `detailDatabase` (the image-form detail database keeps its own declaration unchanged; only the result database becomes host-based).
   Everything else is byte-carried from the green spec. This is a fully valid CR per `validateDatabaseSpec` ("exactly one of image or host") — the readiness probe passes (SELECT 1 against a real PostgreSQL: the red's own detail database), the experiment reaches `InProgress`, standard recipes run, and every runner's result insert fails ("relation does not exist" — the detail database has no result table) → `exit 1` → the Job exhausts `globalBackoffLimit = 4` (≈3 min) → `JobFailed` → `ObservationFailed` → the guarded `InProcessing → Failed`.

2. **The batch**: launch the **unchanged eds-mock one-shot** for the red project — the mock image is the green CR's `spec.experimentalDesignService.image`; run it the way the installation runs it (inspect `test/e2e/manifests/base/stack.yaml` for the eds-mock's env contract — `NATS_URL`, the identity envs — and replicate; `PROJECT_NAME` = `<project>-fcrash`), via the suite's kubectl helper (a `kubectl run`-style one-shot or a small Job manifest applied and deleted by the spec). The mock's own retry behavior covers the window before the red experiment reaches `InProgress`. **Teardown the mock fixture in the spec.**

3. **The chain assertions**:
   - `Eventually` (a generous bound — provisioning ~1 min + the batch + translation + the ~3 min job backoff + the observation ticks; ≥8 minutes is safe): `status.scenarioManagerVerdict == "Failed"` **and** `status.phase == "Failed"`, asserting the verdict is first observed at or before the phase (the report precedes the derivation — mirror the existing terminal-phase spec's ordering idiom);
   - the **PhaseTransition Event**: an event on the red experiment with reason `PhaseTransition` and a message containing `Failed` (the operator's D7 emission);
   - **stickiness**: a follow-up reconcile window (annotation trigger, the suite's idiom) — the phase and verdict stay `Failed`;
   - **the GC cascade on deletion**: delete the red experiment and assert the complete owned set disappears (the databases' and translator's and PPS's Deployments/Services/Secrets, the translator-cfg ConfigMap, the runner ServiceAccount, and the runner Jobs);
   - persist terminal evidence to the artifact directory (phase, message, verdict; the scenario states via the suite's query helper; the runner Job's failure condition as triage).

## Constraints

- **All other specs byte-identical** — insertions only in one file.
- The green experiment and its resources are never touched; the mock fixture and the red experiment are the spec's own and are torn down.
- **No component/platform changes** — no operator/SM/PPS/mock code, no API types, no manifests, `make verify-generated` byte-stable. This slice is test code only.
- No cluster run by you: the tier gate is `make test-fast` (the e2e compile); the live run is the manager's consolidated settlement smoke.
- **No test weakening.**
- **Branch discipline:** commit only to this branch, the trailer `[orchestration: task <task-id> dispatch <dispatch-id>]` with your actual IDs. `artifacts/` is never committed.

## Ownership

Exactly: `test/e2e/smoke_test.go` (one `It` block + local helpers). Any discovered gap outside this ownership stops work with an `ask`.

## Observable acceptance

1. At your **first checkpoint** run and record:
   ```bash
   printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
   ```
   Expected exactly `ai.forge/qwen3.8-27b-nvfp4`. On mismatch: stop, report `failed`.
2. `make test-fast` from the repo root: rc=0 (includes the e2e compile for `test/e2e`).
3. Greps for the report: the new `It(...)` present; the host-form `resultDatabase` delta (host = the red detaildb service, the reused credentials); the mock launch with `PROJECT_NAME` and its teardown; `git diff --numstat` shows one file, insertions only.
4. `worker_done` from the dispatched terminal, exactly once, with: a three-sentence executive summary, both lifecycle IDs, `--outcome succeeded|failed`, the branch HEAD SHA, a one-line diff summary, `--files-modified`, `--report-path` (`artifacts/orchestration/<task-id>-report.md`), and the attestation line. Then idle.
