# P2 — SM aggregation pass: the `scenarioManagerVerdict` report + RBAC delta + D11 gate alignment

**Dispatch record (Wave 2, experiment-phase-aggregation).** Read order: `devlog/changes/experiment-phase-aggregation/FEATURE.md` (§2, §3 D1–D5, D8–D9, D11–D12, §5) → `AGENTS.md` → this spec → code. On contradiction the FEATURE.md cannot resolve through repository inspection: stop and ask via your `ask` channel.

## Target

- New package `scenario-manager/internal/aggregate/` (the aggregation pass)
- `scenario-manager/internal/persistence/` — one new aggregation query (follow the file style of `scenario_status.go`/`project.go`)
- `scenario-manager/internal/lifecycle/lifecycle.go` + gate switch + gate tests (the D11 rename)
- `scenario-manager/internal/rbac/rbac.go` (`RequiredChecks`) + its tests
- `test/e2e/manifests/base/stack.yaml` (the SM Role)
- `scenario-manager/internal/core/app.go` (wiring)

## Change

1. **The aggregation query (persistence).** One function, per project ID, returning the scenario-state counts needed by the D3 rule (total, `Finished` count, `Failed` count) over the configured scenario-status table. Follow the established function style of `scenario_status.go` (context-first, the configured table name, wrapped errors). No other persistence change.

2. **The pass package `internal/aggregate`**, mirroring the established scheduler idiom of `runnerstart`/`observation` (`NewScheduler` + `Start` + `Shutdown`, bounded fixed cadence, no hot loops, idempotent ticks). Per tick:
   - enumerate experiments the way the informer does (`internal/kube` `ListSimulationExperiments`, same namespace scope the informer's watch uses);
   - gate each with `lifecycle.AdmitExperiment` — only `Admit` proceeds (D5: Pending/Provisioning skip, terminal skip, unknown conservatively skips);
   - **skip if `Status.ScenarioManagerVerdict` is already non-empty** (the verdict is absorbing — write-if-absent, D3/D4);
   - resolve the project ID via `persistence.ProjectIDByNamespaceAndName`; a missing row is a no-op for this tick;
   - aggregate the counts and apply **D3**: any scenario `Failed` → verdict `Failed` (fail-fast); else total > 0 and every scenario `Finished` → verdict `Finished`; else (total 0, or not all terminal) → no verdict this tick;
   - write via the **status subresource** with a merge patch whose status payload carries **only** `scenarioManagerVerdict` (D9 field ownership — never `phase`, never `message`);
   - per-experiment failures log in the established `operation=` style and skip to the next experiment; the next tick retries (self-healing).

3. **D11 (gate vocabulary).** Rename `PhaseCompleted = "Completed"` → `PhaseFinished = "Finished"` in `lifecycle.go` (constant, the gate's switch case in `gate.go`, doc comments, and every test reference). Zero behavior change beyond the rename: no writer of the old value exists, and the gate now explicitly recognizes the operator's success-terminal instead of the conservative unknown-phase default.

4. **RBAC delta (D8).** `RequiredChecks()` gains the check `{Group: Alpha4ExperimentGroup, Resource: "simulationexperiments", Verb: "patch", Subresource: "status"}` (the `Check` struct already has the `Subresource` field); update `RequiredChecks`' doc comment (the "deliberately absent" list gains its one exception). `test/e2e/manifests/base/stack.yaml`: the SM Role gains `simulationexperiments/status` `patch` (additive rule). Update `rbac` tests accordingly — a denied check remains a fatal startup error.

5. **Wiring.** `core/app.go` constructs the aggregate scheduler with its dependencies and starts/shuts it down alongside the existing schedulers, in the established style.

6. **Unit tests.** The D3 truth table (fail-fast: one Failed with unfinished present → `Failed`; all-Finished → `Finished`; zero scenarios → never; mixed non-terminal → no-op); the absorbing skip (already-set verdict → no patch); the gate behaviors via `AdmitExperiment` (InProgress admits; Pending/Provisioning/terminal/unknown skip); the patch payload proof (only `scenarioManagerVerdict` — an assertion on the actual patch bytes); the failure taxonomy (project-lookup error → log+skip; patch error → next tick); the D11 gate tests after the rename.

## Constraints

- **Untouched:** the scenario-level machinery (states, rounds, verdict application, evaluation loop), the informer/verdict handler, every NATS surface, the e2e suite (P4 owns it), the operator (P3 owns it). The SM never writes `phase`/`message` (D9).
- **No test weakening** — existing tests extend for the rename and the RBAC delta, never weaken.
- **Additive manifest change** — the stack.yaml Role gains one rule; nothing else in the manifests moves.
- `make verify-generated` must remain byte-stable (P2 touches no API types).
- **Branch discipline:** commit only to this branch (never `main`), every commit carrying the trailer `[orchestration: task <task-id> dispatch <dispatch-id>]` with your actual IDs. `artifacts/` is never committed.

## Ownership

Exactly: the new `aggregate` package, the one persistence query, the lifecycle/gate rename + its tests, the rbac check + its tests, the one stack.yaml rule, and the core/app wiring. Any discovered gap outside this ownership stops work with an `ask`.

## Observable acceptance

1. At your **first checkpoint** run and record:
   ```bash
   printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
   ```
   Expected exactly `ai.forge/qwen3.8-27b-nvfp4`. On mismatch: stop, report `failed`.
2. `make test-fast` from the repo root: rc=0 (verify-generated byte-stable included).
3. Greps for the report: `internal/aggregate` scheduler wired in `core/app.go`; `PhaseFinished` present and `PhaseCompleted` absent repo-wide (SM module); the RBAC status-patch check present; the stack.yaml `simulationexperiments/status` patch rule present; `git diff --stat main...HEAD` shows only the owned files (plus P1's already-landed two).
4. `worker_done` from the dispatched terminal, exactly once, with: a three-sentence executive summary, both lifecycle IDs, `--outcome succeeded|failed`, the branch HEAD SHA, a one-line diff summary, `--files-modified`, `--report-path` (`artifacts/orchestration/<task-id>-report.md`), and the attestation line. Then idle.
