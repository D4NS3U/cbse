# Dispatch prompt — P3B1 canonical `erlang_a_v1` model and CLI

Implement `devlog/changes/pads-paper/slices/P3B1-canonical-erlang-a-model-cli.md` exactly.

## Read first

1. `devlog/changes/pads-paper/FEATURE.md`
2. `AGENTS.md`
3. `devlog/changes/pads-paper/IMPLEMENTATION_HANDOFF.md`
4. `devlog/changes/pads-paper/slices/P3B1-canonical-erlang-a-model-cli.md`
5. `pads-paper/simulation-model/README.md`
6. `devlog/changes/pads-paper/MODEL_SELECTION.md` and `INITIAL_RESULTS_PLAN.md` for terminology only

The feature and slice are normative and read-only. Stop and ask through Orca if implementation details reveal a contradiction.

## Ownership

You may edit only:

- `pads-paper/simulation-model/src/**`
- `pads-paper/simulation-model/README.md`

Do not edit container files, paper-local components, original templates, devlog specifications, shared runtime code, tests outside the canonical model directory, Makefiles, manifests, CRDs, `go.work`, or artifacts.

## Required result

Develop CBSE's original Erlang-A event logic; do not copy third-party GitHub implementation source. Preserve injected-RNG and future-production contracts, implement the strict metrics/result builder, add the credential-free deterministic CLI, and provide robust unit/invariant/ensemble tests.

## Runtime attestation

At the first checkpoint run:

```bash
printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
```

Required implementation runtime: `ai.forge/qwen3.8-27b-nvfp4`. Repeat the exact output in `worker_done`; mismatch is failure.

## Verification

Run every observable acceptance check from the slice, including canonical Python tests, deterministic CLI replay, invalid-input behavior, source inspection, `git diff --check`, and repository-root `make test-fast`. Smoke is not required because this slice adds no image or cluster integration.

Commit only to the dedicated child branch with the assigned Task/Dispatch trailer:

```text
[orchestration: task <task-id> dispatch <dispatch-id>]
```

Report branch HEAD, files modified, algorithm/result summary, exact test receipts, runtime attestation, and any limitation through `worker_done`.
