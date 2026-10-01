# T2 — The readiness watchdog: bounded retries, per-retry inventory, the aggregated Error message (+ the G-ruled PPS gate, + flavor 3)

**Dispatch record (Wave 2, experiment-terminal-e2e).** Read order: `devlog/changes/experiment-terminal-e2e/FEATURE.md` (§3 D3, D5; §5) → `AGENTS.md` → this spec → code. On contradiction the FEATURE.md cannot resolve through repository inspection: stop and ask via your `ask` channel.

## Target

- `experiment-operator/internal/controller/` — `checkReadiness` and the watchdog (a new file in the package is welcome; the reconciler struct gains the injectable knobs)
- `experiment-operator/internal/controller/alpha4/` — the envtest coverage
- `test/e2e/smoke_test.go` — one new `It(...)` (flavor 3), inserted after the two T1 red specs and before the GC spec

## Change

1. **The G-ruled gate extension.** `checkReadiness` gains the PPS Deployment check after the translator's: `Get <exp>-pps`, absent or `ReadyReplicas < 1` → not ready (its own inventory entry, exactly like the translator's). The `InProgress` message "All components provisioned and ready" is now true for the full provisioned set.

2. **The watchdog (ruling R, user-spec'd).** `checkReadiness`'s not-ready path gains a bounded-retry watchdog:
   - **The initial not-ready evaluation** starts the watchdog (no inventory yet) and requeues at the existing 5s cadence (fast polls keep the green path's readiness latency unchanged).
   - **Three counted retries**, each an evaluation occurring **≥60s after the previous counted one** (time-gated counting: the 5s polls between do not count). **Each counted retry collects the not-ready component inventory**: for each not-ready component, its name and observed failure — the probe error string, the absent-Deployment case, `0/1 ready`, and the Deployment's status condition reason where cheaply available.
   - **The third counted retry that still finds not-ready components transitions the experiment to `Error` without further retry** (via the existing `setErrorStatus`/`patchPhase` path), the **final message aggregating the three per-retry inventories** (each labeled by its retry, per the user's ruling: the concrete cause is *the component(s) that did not become ready and their failures at each retry*).
   - **State:** in-memory, keyed by experiment UID — `{lastCountedAt, retries, inventories}`; cleared on `InProgress` (all ready), on the `Error` write, and on the experiment's deletion; stale-entry hygiene documented (no unbounded growth). **Restart semantics documented in the code comments:** an operator restart clears the map and the budget restarts (deliberate: no API surface change, no persisted deadline).
   - **Testability (required):** the spacing and the retry count are injectable on the reconciler struct (mirroring the `DBProbe` nil-default pattern) — e.g. `ReadinessRetryInterval time.Duration` (default 60s) and `ReadinessMaxRetries` (default 3); envtest injects milliseconds. Production defaults are compiled in; the e2e path uses them.

3. **Envtest coverage** (extend the alpha4 suite): (a) a not-ready component across the budget → `Error` with the aggregated message (assert the per-retry labels and the component naming); (b) a component becoming ready mid-budget → `InProgress`, state cleared, no false `Error`; (c) a not-ready **PPS** blocks `InProgress` (the G-ruled gate) and a never-ready PPS drives the watchdog to `Error` with the PPS in the inventory; (d) a healthy fast rollout reaches `InProgress` unaffected (the watchdog never fires).

4. **The flavor-3 red spec** (`test/e2e/smoke_test.go`, one `It`, built like T1's: deep-copy the live green experiment, name `<project>-errready`, exactly one delta — `translator.image` set to a **well-formed digest reference that does not exist** (e.g. a syntactically valid `name@sha256:<64 hex>` pointing at an unreachable registry), so validation passes, the Deployment creates, the pull fails observably): assert `Eventually` (a generous bound ≥4 minutes) `status.phase == "Error"`; the message names the **translator** and its observed failure with the per-retry labels; the **full-but-not-ready child set** exists (the databases healthy and probed ready — their images are the green ones — plus the translator and PPS Deployments present, none of which gates blocked beyond the translator); stickiness across a follow-up reconcile; delete → the GC cascade removes the complete owned set; terminal evidence persisted to the artifact directory per the suite's discipline.

## Constraints

- **No API/CRD change** (the watchdog is operator-internal; the state is in-memory per the ruling); no SM/PPS code; no manifest changes; `make verify-generated` stays byte-stable.
- **The green e2e specs stay byte-identical** — the watchdog must not slow the green path (the 5s poll cadence is unchanged; only counting is time-gated). The only e2e edit is the one flavor-3 insertion.
- **No test weakening.** The existing envtest suite extends; no existing assertion is relaxed. If an existing test pins the 5s-only requeue behavior, diagnose at the source and adapt the test honestly (the watchdog changes that behavior by design — the adaptation must assert the new contract, not conceal it).
- **Branch discipline:** commit only to this branch, the trailer `[orchestration: task <task-id> dispatch <dispatch-id>]` with your actual IDs. `artifacts/` is never committed.

## Ownership

Exactly: the controller package (the watchdog + the gate's PPS check + the knobs), the alpha4 envtest suite, and the one e2e insertion. Any discovered gap outside this ownership stops work with an `ask`.

## Observable acceptance

1. At your **first checkpoint** run and record:
   ```bash
   printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
   ```
   Expected exactly `ai.forge/qwen3.8-27b-nvfp4`. On mismatch: stop, report `failed`.
2. `make test-fast` from the repo root: rc=0 (verify-generated byte-stable; the operator envtest suite runs inside it).
3. Greps for the report: the PPS check in `checkReadiness`; the watchdog state + the three-retry counting + the inventory collection + the aggregated message; the injectable knobs with 60s/3 defaults; the four envtest cases; the flavor-3 `It` present; `git diff --numstat` shows only the owned files (insertions in smoke_test.go; the controller/test files as needed).
4. `worker_done` from the dispatched terminal, exactly once, with: a three-sentence executive summary, both lifecycle IDs, `--outcome succeeded|failed`, the branch HEAD SHA, a one-line diff summary, `--files-modified`, `--report-path` (`artifacts/orchestration/<task-id>-report.md`), and the attestation line. Then idle.
