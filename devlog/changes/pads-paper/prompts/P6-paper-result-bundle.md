# Dispatch prompt — P6 paper-result bundle tooling

Implement `devlog/changes/pads-paper/slices/P6-paper-result-bundle.md` exactly.

## Read first

1. `devlog/changes/pads-paper/FEATURE.md`
2. `AGENTS.md`
3. `devlog/changes/pads-paper/IMPLEMENTATION_HANDOFF.md`
4. `devlog/changes/pads-paper/slices/P6-paper-result-bundle.md`
5. `devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md` (the experiments being implemented)
6. `pads-paper/acceptance/**` (the driver you extend and the tools you reuse), the committed P5 evidence under `pads-paper/acceptance/evidence/pads-20261105154051-121fcd/` (the export formats you must stay compatible with), the EDS server API, the scenario-manager Core DB schema, and `pads-paper/simulation-model/src/`

The feature and slice are normative and read-only. Stop and ask through Orca if repository inspection reveals a contradiction (for example an export field or Core DB column the analysis cannot pin down from source).

## Ownership

You may create or edit only:

- `pads-paper/results/**` (new tree)
- `pads-paper/acceptance/run-experiment.sh` (the ruled `--design` extension; default path unchanged)
- `pads-paper/acceptance/README.md` (document the extension)
- `pads-paper/acceptance/test_acceptance.py` (design-mode tests; existing tests stay green)

Do not edit devlog specifications, the shared harness, e2e manifests, component code, `pads-paper/simulation-model/**`, or artifacts that are not your own tooling's outputs.

## Required result

Author the normative campaign design, the design-driven driver extension (multi-scenario EDS flow, per-scenario export/assert/replay, per-scenario sanitized evidence, campaign timeout), the stdlib analysis tool with unit tests, the pinned matplotlib figure tool with deterministic SVG output, the limitations document, and the results README. You do NOT execute any cluster run and do NOT fabricate campaign data: `pads-paper/results/evidence/` receives only a placeholder README.

## Runtime attestation

At the first checkpoint run:

```bash
printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
```

Required implementation runtime: `ai.forge/qwen3.8-27b-nvfp4`. Repeat the exact output in `worker_done`; mismatch is failure.

## Verification

Run every worker-lane observable acceptance check from the slice: `bash -n`; `python3 -m unittest` in both `pads-paper/acceptance` and `pads-paper/results/analysis` (prove matplotlib-dependent tests and SVG byte-stability through a temporary untracked venv; SimPy-gated skips follow the existing convention); refusal paths and the default-mode stub dry-run unchanged and green; the multi-scenario design-mode stub dry-run green with the ruled evidence structure and sanitization; `git diff --check`; and repository-root `make test-fast`. No cluster access, no image build, no smoke.

Commit only to the dedicated child branch with the assigned Task/Dispatch trailer:

```text
[orchestration: task <task-id> dispatch <dispatch-id>]
```

Report branch HEAD, files modified, the exact offline receipts, runtime attestation, and any limitation through `worker_done`.
