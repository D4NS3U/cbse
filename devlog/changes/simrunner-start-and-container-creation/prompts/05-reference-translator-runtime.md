# Slice 05 implementation prompt: Reference Translator runtime

Implement Slice 05 only. Leave the worktree reviewable and resumable; do not begin Slice 06 in this run.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns the cross-cutting contracts.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read Slices 01 through 05 completely, ending with the target contract in [`../slices/05-reference-translator-runtime.md`](../slices/05-reference-translator-runtime.md).

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, relevant diffs, the Operator-provisioned Translator contract, namespace-aware messaging, existing Translator mock behavior, image tooling, and database fixtures. Verify Slices 01 through 04 in numeric order against their completion criteria and required test evidence. If any earlier slice is incomplete or verification-blocked, stop at the earliest such slice and do not implement Slice 05.

## Required outcome

Implement every Slice 05 requirement and acceptance criterion. Deliver the production-quality reference framework and replaceable example generator, not a universal simulation model: validate startup configuration without leaking values, wait for rootless BuildKit readiness before attaching the consumer, process requests serially, perform bounded Detail DB lookup, generate the specified runner context, build and push through the mounted registry configuration, resolve and persist immutable outcomes, recover without repeated work, publish ready before acknowledging, and implement the generated runner's Result DB behavior. Add the owned Translator and reference Scenario Detail Database images, locks, SQL, tests, and integration documentation required by the slice.

Do not introduce concurrent builds, credential reload, remote BuildKit, insecure registry access, untrusted-build isolation claims, production image garbage collection, or changes to the established payload and acknowledgement contracts.

## Execution and verification rules

- Keep framework, generator, database endpoint, build, persistence, and messaging boundaries independently testable.
- Preserve user changes. Do not reset destructive state, expose secrets, or weaken tests. Do not create commits.
- Run focused Go tests with the race detector where required, plus generator, database, BuildKit-client, durable-recovery, image-validation, and acknowledgement-order tests.
- Before claiming completion, run repository-root `make test-fast` and the mandatory `make test-smoke` under the exact current `AGENTS.md` safety contract.
- If smoke prerequisites are unavailable, finish all safe implementation and fast checks, mark Slice 05 `verification-blocked`, and stop without beginning Slice 06.

## Final handoff

Report: prerequisite audit; Slice 05 status (`complete`, `incomplete`, or `verification-blocked`); framework, image, generator, and persistence behavior implemented; files changed; every test command and result; remaining acceptance criteria or blockers; relevant uncommitted worktree state; confirmation that no commit was created; and `Resume at: Slice 05` unless the full slice and mandatory verification passed, in which case use `Resume at: Slice 06`.
