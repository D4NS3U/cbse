# Slice 05 implementation prompt: Reference Translator runtime

Implement Slice 05 only. Leave the worktree reviewable and resumable; do not begin Slice 06 in this run.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns the cross-cutting contracts.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read [`../IMPLEMENTATION_HANDOFF.md`](../IMPLEMENTATION_HANDOFF.md) completely and validate it against the current worktree.
4. Read the target contract in [`../slices/05-reference-translator-runtime.md`](../slices/05-reference-translator-runtime.md) completely. Read an earlier slice completely only if its completion evidence is missing, this work changes its contract, or a failure points back to it.

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, relevant diffs, the Operator-provisioned Translator contract, namespace-aware messaging, existing Translator mock behavior, image tooling, and database fixtures. Verify recorded completion evidence for Slices 01 through 04 in numeric order against relevant code and tests without automatically rereading or rerunning all prior work. If any earlier slice is incomplete or verification-blocked, stop at the earliest stable ID and do not implement Slice 05. Otherwise update the handoff with the base commit, first incomplete Slice 05 ID, and `in-progress` state.

## Required outcome

Implement local milestones `S05-M1` through `S05-M4`, local acceptance groups `S05-A1` through `S05-A3`, and the Slice 05 completion-and-handoff criteria. `S05-D07` defines the later full-smoke template selection and does not block Slice 05. Deliver the production-quality reference framework and replaceable example generator, not a universal simulation model: validate startup configuration without leaking values, wait for rootless BuildKit readiness before attaching the consumer, process requests serially, perform bounded Detail DB lookup, generate the specified runner context, build and push through the mounted registry configuration, resolve and persist immutable outcomes, recover without repeated work, publish ready before acknowledging, and implement the generated runner's Result DB behavior. Add the owned Translator and reference Scenario Detail Database images, locks, SQL, tests, and integration documentation required by the slice.

Do not introduce concurrent builds, credential reload, remote BuildKit, insecure registry access, untrusted-build isolation claims, production image garbage collection, or changes to the established payload and acknowledgement contracts.

## Execution and verification rules

- Keep framework, generator, database endpoint, build, persistence, and messaging boundaries independently testable.
- Preserve user changes. Do not reset destructive state, expose secrets, or weaken tests. Do not create commits.
- Run focused Go tests with the race detector where required, plus generator, database, BuildKit-client, durable-recovery, image-validation, and acknowledgement-order tests.
- Before claiming completion, run repository-root `make test-fast` and the mandatory `make test-smoke` under the exact current `AGENTS.md` safety contract.
- If smoke prerequisites are unavailable, finish all safe implementation and fast checks, mark Slice 05 `verification-blocked`, and stop without beginning Slice 06.

## Final handoff

Update `IMPLEMENTATION_HANDOFF.md`, then report: prerequisite audit; Slice 05 status (`complete`, `incomplete`, or `verification-blocked`); stable IDs and framework/image/generator/persistence behavior completed; files changed; every test command and result; remaining stable ID or blocker; relevant uncommitted worktree state; confirmation that no commit was created; and the exact stable ID at which to resume.
