# Slice 04 implementation prompt: Scenario Manager messaging and lifecycle

Implement Slice 04 only. Leave the worktree reviewable and resumable; do not begin Slice 05 in this run.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns the cross-cutting contracts.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read Slices 01 through 04 completely, ending with the target contract in [`../slices/04-sm-messaging-and-lifecycle.md`](../slices/04-sm-messaging-and-lifecycle.md).

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, relevant diffs, current Scenario Manager NATS, Core DB, EDS, informer, startup, shutdown, and lifecycle code. Verify Slices 01 through 03 in numeric order against their completion criteria and required test evidence, even though the dependency map lists only Slice 01 as Slice 04's direct technical dependency. If any earlier slice is incomplete or verification-blocked, stop at the earliest such slice and do not implement Slice 04.

## Required outcome

Implement every Slice 04 requirement and acceptance criterion. Make namespace/name routing, exact project persistence, shared messaging artifacts, publication boundaries, incarnation-aware lifecycle gates, terminal actions, finalizer behavior, and deletion cleanup converge safely across retries, redelivery, stale informer events, process restarts, and concurrent Scenario Manager replicas. Preserve unchanged JSON payload schemas and shared subscriptions while preventing one experiment from mutating another experiment's durable state or messaging artifacts.

Do not add NATS authentication, per-project unsubscription, orphan sweeping, experiment-phase aggregation, compatibility migration, or runner orchestration owned by Slice 06.

## Execution and verification rules

- Keep external calls, guarded database transitions, cancellation, and cleanup ordering explicit and independently testable.
- Preserve user changes. Do not reset destructive state, expose secrets, or weaken tests. Do not create commits.
- Run focused NATS, Core DB, EDS, lifecycle-gate, finalizer, cancellation, concurrency, and integration tests during development.
- Before claiming completion, run repository-root `make test-fast` and the mandatory `make test-smoke` under the exact current `AGENTS.md` safety contract.
- If smoke prerequisites are unavailable, finish all safe implementation and fast checks, mark Slice 04 `verification-blocked`, and stop without beginning Slice 05.

## Final handoff

Report: prerequisite audit; Slice 04 status (`complete`, `incomplete`, or `verification-blocked`); messaging and lifecycle behavior implemented; files changed; every test command and result; remaining convergence cases or blockers; relevant uncommitted worktree state; confirmation that no commit was created; and `Resume at: Slice 04` unless the full slice and mandatory verification passed, in which case use `Resume at: Slice 05`.
