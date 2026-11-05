# Dispatch prompt — P5 end-to-end acceptance tooling

Implement `devlog/changes/pads-paper/slices/P5-end-to-end-acceptance.md` exactly.

## Read first

1. `devlog/changes/pads-paper/FEATURE.md`
2. `AGENTS.md`
3. `devlog/changes/pads-paper/IMPLEMENTATION_HANDOFF.md`
4. `devlog/changes/pads-paper/slices/P5-end-to-end-acceptance.md`
5. `test/harness/smoke.sh` and `test/harness/build-images.sh` (the conventions you mirror), `test/e2e/manifests/base` + `experiment.yaml` (the stack you render), `pads-paper/experimental-design-service/component/internal/server/server.go` (the EDS API), the scenario-manager Core DB schema (for the evaluation export fields), and `pads-paper/simulation-model/src/` (the canonical model for replay)

The feature and slice are normative and read-only. Stop and ask through Orca if repository inspection reveals a contradiction (for example an EDS payload shape or Core DB column the slice cannot pin down from source).

## Ownership

You may create or edit only `pads-paper/acceptance/**`. Do not edit devlog specifications, the shared harness, e2e manifests, component code, or artifacts that are not your own tooling's outputs.

## Required result

Author the publication script, the live experiment driver, the assertion and replay tools with self-tests, and the acceptance README (including the evidence and sanitization policy). The driver must follow the ruled preflight/deploy/design/export/teardown sequence and must never mutate the cluster when its environment is incomplete. You do NOT execute any cluster run and do NOT create evidence content: live runs are settlement-owned by the coordinator; the `evidence/` directory receives only a `.gitkeep`-style placeholder README (no fabricated run data).

## Runtime attestation

At the first checkpoint run:

```bash
printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
```

Required implementation runtime: `ai.forge/qwen3.8-27b-nvfp4`. Repeat the exact output in `worker_done`; mismatch is failure.

## Verification

Run every worker-lane observable acceptance check from the slice: `bash -n` (and shellcheck if installed), refusal-path proofs with cleared environment, `python3 -m unittest` in `pads-paper/acceptance` (prove the replay self-tests fully through a temporary untracked SimPy 4.1.2 venv), `git diff --check`, and repository-root `make test-fast`. No cluster access, no image build, no smoke.

Commit only to the dedicated child branch with the assigned Task/Dispatch trailer:

```text
[orchestration: task <task-id> dispatch <dispatch-id>]
```

Report branch HEAD, files modified, the exact offline receipts, runtime attestation, and any limitation through `worker_done`.
