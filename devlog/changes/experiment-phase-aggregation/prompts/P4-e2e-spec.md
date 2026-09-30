# P4 — e2e spec: the live Finished chain (verdict → experiment phase)

**Dispatch record (Wave 4, experiment-phase-aggregation — the final implementation wave).** Read order: `devlog/changes/experiment-phase-aggregation/FEATURE.md` (§3 D10, D12; §5, §7) → `AGENTS.md` → this spec → code. On contradiction the FEATURE.md cannot resolve through repository inspection: stop and ask via your `ask` channel.

## Target

- `test/e2e/smoke_test.go` — exactly one new `It(...)` spec, inserted after the convergence spec (`It("converges all four scenarios: met-path single-wave bookkeeping and loop-path natural top-up")`, around line 358–532) and before the idempotent-metadata spec.

## Change

The seventh smoke spec — **"derives the experiment's terminal phase from the scenario-aggregate verdict"** — proving the live Finished chain end-to-end (D10):

1. **The verdict and the phase.** After the convergence spec has driven all four scenarios to `Finished`, `Eventually` (the suite's established `k8sClient.Get` + Ginkgo idiom, poll ~2s, a generous bound of ~2 minutes — the aggregate pass's 5s cadence means one tick of verdict latency, the operator's watch reacts immediately): `experiment.Status.ScenarioManagerVerdict == "Finished"` **and** `experiment.Status.Phase == "Finished"` — assert the verdict first appearing at or before the phase (the report precedes the derivation; asserting both in one Eventually is acceptable, but the spec must observe both values, not just the phase).
2. **Stickiness under a follow-up reconcile.** After the terminal phase is reached, prove no regression: the phase remains `Finished` (and the verdict remains `"Finished"`) across a follow-up reconcile window — follow the suite's established idiom (a bounded `Consistently`-style window, or an explicit reconcile trigger consistent with how the idempotent-metadata spec triggers reconciles). The absorbing semantics (D3) and the parked default case (D6) guarantee this; the spec makes it observable.
3. **Diagnostics artifact.** Following the suite's triage discipline (the `writeDatabaseArtifact` pattern), persist the terminal experiment status (phase, message, verdict) to the artifact directory.

## Constraints

- **All other specs byte-identical:** the diff is exactly one insertion in one file — `git diff` must show no other change to `smoke_test.go` and no change to any other file.
- **No cluster run by you:** the live execution is the manager's consolidated settlement gate (`make test-smoke`); your tier gate is `make test-fast` (the e2e compile checks run inside it). Do not attempt a cluster run; report the spec as compile-proven, live-pending-settlement.
- **No test weakening** anywhere; the suite grows one spec, none is modified.
- **Branch discipline:** commit only to this branch (never `main`), the commit carrying the trailer `[orchestration: task <task-id> dispatch <dispatch-id>]` with your actual IDs. `artifacts/` is never committed.

## Ownership

Exactly: one new `It` block in `test/e2e/smoke_test.go` (plus, if genuinely required, a small unexported helper local to the file — anything beyond that stops work with an `ask`).

## Observable acceptance

1. At your **first checkpoint** run and record:
   ```bash
   printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
   ```
   Expected exactly `ai.forge/qwen3.8-27b-nvfp4`. On mismatch: stop, report `failed`.
2. `make test-fast` from the repo root: rc=0 (includes `go vet -tags=e2e` and the e2e compile for `test/e2e`).
3. Greps for the report: the new `It("derives the experiment's terminal phase...")` present; `git diff e1f6000..HEAD -- test/e2e/smoke_test.go` shows only insertions; `git diff --stat e1f6000..HEAD` shows exactly one file.
4. `worker_done` from the dispatched terminal, exactly once, with: a three-sentence executive summary, both lifecycle IDs, `--outcome succeeded|failed`, the branch HEAD SHA, a one-line diff summary, `--files-modified`, `--report-path` (`artifacts/orchestration/<task-id>-report.md`), and the attestation line. Then idle.
