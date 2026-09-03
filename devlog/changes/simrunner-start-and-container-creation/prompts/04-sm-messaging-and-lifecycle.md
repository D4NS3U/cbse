# Slice 04 implementation prompt: Scenario Manager messaging and lifecycle

Implement Slice 04 only. Leave the worktree reviewable and resumable; do not begin Slice 05 in this run.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns the cross-cutting contracts.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read [`../IMPLEMENTATION_HANDOFF.md`](../IMPLEMENTATION_HANDOFF.md) completely and validate it against the current worktree.
4. Read the target contract in [`../slices/04-sm-messaging-and-lifecycle.md`](../slices/04-sm-messaging-and-lifecycle.md) and incoming deferred group `S01-D04` completely. Read an earlier slice completely only if its completion evidence is missing, this work changes its contract, or a failure points back to it.

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, relevant diffs, current Scenario Manager NATS, Core DB, EDS, informer, startup, shutdown, and lifecycle code. Verify recorded completion evidence for Slices 01 through 03 in numeric order against relevant code and tests, even though the dependency map lists only Slice 01 as Slice 04's direct technical dependency. Do not automatically reread or rerun all prior work. If any earlier slice is incomplete or verification-blocked, stop at the earliest stable ID and do not implement Slice 04. Otherwise update the handoff with the base commit, first incomplete Slice 04 ID, and `in-progress` state.

## Required outcome

Implement incoming group `S01-D04`, local milestones `S04-M1` through `S04-M4`, local acceptance groups `S04-A1` through `S04-A4`, and the Slice 04 completion-and-handoff criteria. `S04-D06` defines later runner-create integration and does not block Slice 04. Make namespace/name routing, exact project persistence, shared messaging artifacts, publication boundaries, incarnation-aware lifecycle gates, terminal actions, finalizer behavior, and deletion cleanup converge safely across retries, redelivery, stale informer events, process restarts, and concurrent Scenario Manager replicas. Preserve unchanged JSON payload schemas and shared subscriptions while preventing one experiment from mutating another experiment's durable state or messaging artifacts.

Do not add NATS authentication, per-project unsubscription, orphan sweeping, experiment-phase aggregation, compatibility migration, or runner orchestration owned by Slice 06.

## Execution and verification rules

- Keep external calls, guarded database transitions, cancellation, and cleanup ordering explicit and independently testable.
- Preserve user changes. Do not reset destructive state, expose secrets, or weaken tests. Do not create commits.
- Run focused NATS, Core DB, EDS, lifecycle-gate, finalizer, cancellation, concurrency, and integration tests during development.
- Before claiming completion, run repository-root `make test-fast` and the mandatory `make test-smoke` under the exact current `AGENTS.md` safety contract.
- If smoke prerequisites are unavailable, finish all safe implementation and fast checks, mark Slice 04 `verification-blocked`, and stop without beginning Slice 05.

## Final handoff

Update `IMPLEMENTATION_HANDOFF.md`, then report: prerequisite audit; Slice 04 status (`complete`, `incomplete`, or `verification-blocked`); stable IDs and messaging/lifecycle behavior completed; files changed; every test command and result; remaining stable ID or blocker; relevant uncommitted worktree state; confirmation that no commit was created; and the exact stable ID at which to resume.
