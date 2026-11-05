# Dispatch prompt — P2 paper component workspace foundation

Implement `devlog/changes/pads-paper/slices/P2-paper-component-workspace.md` exactly.

## Read first

1. `devlog/changes/pads-paper/FEATURE.md`
2. `AGENTS.md`
3. `devlog/changes/pads-paper/slices/P2-paper-component-workspace.md`
4. Existing component READMEs only as needed to document accurate preserved contracts

The feature and slice are normative and read-only. Stop and ask through Orca if repository inspection reveals a contradiction.

## Ownership

You may create and edit only `pads-paper/**`. Do not edit `devlog/**`, `component-templates/**`, shared runtime code, tests, manifests, CRDs, Makefiles, or artifacts.

## Required result

Create the exact tracked README-only directory tree and document ownership, baseline provenance, component responsibilities, preserved contracts, and the mandatory template-reuse ruling. This slice creates no executable code, Dockerfile, symlink, copied module, or generated content.

## Runtime attestation

At your first checkpoint run:

```bash
printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
```

The required implementation runtime is `ai.forge/qwen3.8-27b-nvfp4`. Repeat the exact output in `worker_done`; a mismatch is failure.

## Verification

Run all observable acceptance checks from the slice, including `git diff --check` and repository-root `make test-fast`. Smoke is not required because P2 is documentation-only.

Commit only to your dedicated child branch with a trailer exactly naming the assigned Task and Dispatch:

```text
[orchestration: task <task-id> dispatch <dispatch-id>]
```

Report branch HEAD, files modified, exact test results, runtime attestation, and any limitation through `worker_done`.
