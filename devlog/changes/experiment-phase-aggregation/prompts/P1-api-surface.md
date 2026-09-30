# P1 — API surface: `scenarioManagerVerdict` + the `Finished` phase vocabulary

**Dispatch record (Wave 1, experiment-phase-aggregation).** Read order: `devlog/changes/experiment-phase-aggregation/FEATURE.md` → `AGENTS.md` → this spec → code. On contradiction the FEATURE.md cannot resolve through repository inspection: stop and ask via your `ask` channel.

## Target

- `experiment-operator/api/alpha4/simulationexperiment_types.go` — the `SimulationExperimentStatus` struct (around line 252) and its doc comment
- Regenerated artifacts: `experiment-operator/zz_generated.deepcopy.go` and `experiment-operator/config/crd/bases/experiment.cbse.terministic.de_simulationexperiments.yaml`

## Change

1. **The new report field (D1/D2/D9).** `SimulationExperimentStatus` gains:

   ```go
   ScenarioManagerVerdict string `json:"scenarioManagerVerdict,omitempty"`
   ```

   with the marker `// +kubebuilder:validation:Enum=Finished;Failed` directly above it and a doc comment stating the field-ownership contract: **written by the Scenario Manager** (the scenario-aggregate verdict over its Core Database), **consumed by the Experiment Operator** (the sole input to the terminal phase derivation); the field is optional and absent until the SM writes its first verdict — absence means *not yet reported*, never user input, never an alternative transition path; once written the verdict is absorbing and never changes.

2. **The `phase` vocabulary (D12).** The existing `Phase` field's marker `// +kubebuilder:validation:Enum=Pending;Provisioning;InProgress;Completed;Failed;Error` gains `Finished` (append; keep `Completed` — it has never been written but removing it would be a schema breaking change). Update the struct's doc comment so the phase vocabulary list includes `Finished` and the verdict field is described.

3. **Regenerate** via the repo's controller-gen flow: from `experiment-operator/`, `make generate manifests`. The CRD gains `status.properties.scenarioManagerVerdict` (string enum `Finished;Failed`) and the extended `phase` enum; deepcopy gains the new field.

## Constraints

- **Additive only.** No removals, renames, or type changes to existing fields; the only existing-field touch is the `Phase` enum marker (D12) and doc comments.
- **Generated files are generator-written only** — never hand-edit `zz_generated.deepcopy.go` or the CRD yaml; the regen must be byte-stable under `make verify-generated`.
- **No controller or SM code** — the reconcile derive is P3's slice; the SM aggregation is P2's. No test-suite edits: if an existing test fails because of this addition, diagnose at the source and stop-and-ask if a test would need weakening (it must not be weakened).
- **Field ownership is the contract** (D9): the SM writes `scenarioManagerVerdict` only; the operator writes `phase`/`message` only. The comments must say so.
- **Branch discipline:** you are in an Orca-managed worktree on the feature branch — commit **only to this branch** (never `main`), every commit carrying the trailer `[orchestration: task <task-id> dispatch <dispatch-id>]` with your actual Task and Dispatch IDs from the injected preamble. Commit exactly: the types file + the two regenerated artifacts. Nothing else is committed (`artifacts/` is ignored).
- Obey `AGENTS.md` (test contract, cluster safety incorporated by reference).

## Ownership

Exactly three committed files: the types file (status struct, markers, doc comments) and the two regenerated artifacts. Any discovered gap outside this ownership stops work with an `ask`.

## Observable acceptance

1. At your **first checkpoint** run and record:
   ```bash
   printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
   ```
   Expected exactly `ai.forge/qwen3.8-27b-nvfp4`. On mismatch: stop, report `failed`.
2. `make verify-generated` from the repo root: rc=0, byte-stable.
3. `make test-fast` from the repo root: rc=0.
4. Greps for the report: `scenarioManagerVerdict` with `omitempty` + enum marker in the types file; `scenarioManagerVerdict` present under `status.properties` in the CRD yaml with `enum: [Finished, Failed]`-equivalent schema; `Finished` present in the `phase` enum in the same yaml; `git diff --stat main...HEAD` shows exactly the three files.
5. `worker_done` from the dispatched terminal, exactly once, with: a three-sentence executive summary, both lifecycle IDs, `--outcome succeeded|failed`, the branch HEAD SHA, a one-line diff summary, `--files-modified` (the three files), `--report-path` (write your report to `artifacts/orchestration/<task-id>-report.md` in the worktree), and the attestation line. Then idle.
