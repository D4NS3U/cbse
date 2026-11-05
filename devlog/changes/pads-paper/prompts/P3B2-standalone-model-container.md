# Dispatch prompt — P3B2 standalone model container

Implement `devlog/changes/pads-paper/slices/P3B2-standalone-model-container.md` exactly.

## Read first

1. `devlog/changes/pads-paper/FEATURE.md`
2. `AGENTS.md`
3. `devlog/changes/pads-paper/IMPLEMENTATION_HANDOFF.md`
4. `devlog/changes/pads-paper/slices/P3B2-standalone-model-container.md`
5. `pads-paper/simulation-model/README.md`, `pads-paper/simulation-model/container/README.md`, and the canonical `src/` modules

The feature and slice are normative and read-only. Stop and ask through Orca if repository inspection reveals a contradiction.

## Ownership

You may create or edit only:

- `pads-paper/simulation-model/container/app/**`
- `pads-paper/simulation-model/container/README.md`
- `pads-paper/simulation-model/README.md` (only the build/run documentation addition)

Do not edit `src/**`, `container/base/**`, other paper-local components, original templates, devlog specifications, shared code, manifests, CRDs, Makefiles, harness scripts, tests, `go.work`, or artifacts.

## Required result

Author the application Dockerfile per P3B2-R1, build the locked two-stage chain locally per P3B2-R2 (Docker is available on this host; start it if the daemon is down), prove every in-container behavior per P3B2-R3, and document the chain per P3B2-R4. Use placeholder local tags in documentation; never record private infrastructure values.

## Runtime attestation

At the first checkpoint run:

```bash
printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"
```

Required implementation runtime: `ai.forge/qwen3.8-27b-nvfp4`. Repeat the exact output in `worker_done`; mismatch is failure.

## Verification

Run every observable acceptance check from the slice: both builds, missing-arg failure, deterministic in-container replay, invalid-input behavior, non-root proof, exact `/runner` content, byte-identity of copied modules, `git diff --check`, and repository-root `make test-fast`. Do not run the cluster smoke tier — that is the coordinator-run settlement gate with user-provided cluster environment.

Commit only to the dedicated child branch with the assigned Task/Dispatch trailer:

```text
[orchestration: task <task-id> dispatch <dispatch-id>]
```

Report branch HEAD, files modified, the recorded local image digests/IDs (they are local-only), exact verification receipts, runtime attestation, and any limitation through `worker_done`.
