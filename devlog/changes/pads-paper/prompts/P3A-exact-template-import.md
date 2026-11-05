# Dispatch prompt — P3A exact template import

Implement `devlog/changes/pads-paper/slices/P3A-exact-template-import.md` exactly.

## Read first

1. `devlog/changes/pads-paper/FEATURE.md`
2. `AGENTS.md`
3. `devlog/changes/pads-paper/IMPLEMENTATION_HANDOFF.md`
4. `devlog/changes/pads-paper/slices/P3A-exact-template-import.md`
5. The current `pads-paper/**/README.md` ownership documents

The feature and slice are normative and read-only. Stop and ask through Orca if baseline extraction or repository inspection contradicts them.

## Ownership

You may create or edit only `pads-paper/**`. Do not edit `devlog/**`, original `component-templates/**`, shared runtime code, tests, manifests, CRDs, Makefiles, `go.work`, or artifacts.

## Required result

Import the exact baseline component trees, selected canonical model files, and runner-base files from commit `1dfc7eb5fec358199cb9c28b1df2aaabb035c71a`; add the provenance record and minimal ownership-document links. Make no behavioral change and do not implement Erlang-A yet.

Use Git-aware extraction (`git archive`, `git show`, and mode inspection) so ignored caches or local files cannot enter the import. Do not use broad filesystem copying that can capture build outputs.

## Runtime attestation

At the first checkpoint run:

```bash
printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
```

The required implementation runtime is `ai.forge/qwen3.8-27b-nvfp4`. Repeat the exact output in `worker_done`; a mismatch is failure.

## Verification

Run all observable acceptance checks from the slice, including reproducible byte/file-list/mode parity against the baseline commit, `git diff --check`, and repository-root `make test-fast`. Smoke is not required because P3A does not build, select, or deploy the dormant imported Dockerfiles.

Commit only to the dedicated child branch with the assigned Task/Dispatch trailer:

```text
[orchestration: task <task-id> dispatch <dispatch-id>]
```

Report branch HEAD, files modified/counts, parity evidence, exact tests, runtime attestation, and limitations through `worker_done`.
