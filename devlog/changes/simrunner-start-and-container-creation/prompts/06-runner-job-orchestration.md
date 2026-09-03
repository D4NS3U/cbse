# Slice 06 implementation prompt: Runner Job orchestration

Implement Slice 06 only. Leave the worktree reviewable and resumable; do not begin Slice 07 in this run.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns the cross-cutting contracts.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read [`../IMPLEMENTATION_HANDOFF.md`](../IMPLEMENTATION_HANDOFF.md) completely and validate it against the current worktree.
4. Read the target contract in [`../slices/06-runner-job-orchestration.md`](../slices/06-runner-job-orchestration.md) and incoming deferred groups `S01-D06`, `S02-D06`, and `S04-D06` completely. Read an earlier slice completely only if its completion evidence is missing, this work changes its contract, or a failure points back to it.

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, relevant diffs, Scenario Manager selection and startup paths, Core DB guarded transitions, lifecycle gates, Kubernetes adapters, scheduling, RBAC assumptions, and Job fixtures. Verify recorded completion evidence for Slices 01 through 05 in numeric order against relevant code and tests, even where the dependency map lists a smaller direct dependency set. Do not automatically reread or rerun all prior work. If any earlier slice is incomplete or verification-blocked, stop at the earliest stable ID and do not implement Slice 06. Otherwise update the handoff with the base commit, first incomplete Slice 06 ID, and `in-progress` state.

## Required outcome

Implement incoming groups `S01-D06`, `S02-D06`, and `S04-D06`, local milestones `S06-M1` through `S06-M7`, local acceptance groups `S06-A1` through `S06-A5`, and the Slice 06 completion-and-handoff criteria. `S06-D07` defines later cross-component smoke, RBAC-manifest, and identity integration and does not block Slice 06. Separate runner-start work from the serial BSSL selector; validate startup configuration before workers begin; discover and dispatch eligible positive IDs in bounded ascending order; de-duplicate ready, delayed, and in-flight work; build effective indexed Jobs from already-validated templates; and make database state the durable authority. Apply the exact successful-create, `AlreadyExists`, ownership-verification, gate-race cleanup, retry, cancellation, restart, observation, compressed-index parsing, monotonic repetition, terminal-state, and verified-deletion contracts from the feature and slice.

Do not re-run the Operator's template policy in Scenario Manager, validate the object returned by a successful Job create, add a second scheduler, process `PostProcessing`, add capacity admission, or invent workload self-healing beyond the specified retry budget.

## Execution and verification rules

- Keep scheduler, Kubernetes adapter, effective-Job builder, observation, lifecycle, and Core DB transitions independently testable.
- Preserve user changes. Do not reset destructive state, expose secrets, or weaken tests. Do not create commits.
- Run focused unit and integration tests for ordering, concurrency, transient failures, ownership collisions, stale claims, lifecycle closure, restart recovery, Job status contradictions, and maximum repetition bounds.
- Before claiming completion, run repository-root `make test-fast` and the mandatory `make test-smoke` under the exact current `AGENTS.md` safety contract.
- If smoke prerequisites are unavailable, finish all safe implementation and fast checks, mark Slice 06 `verification-blocked`, and stop without beginning Slice 07.

## Final handoff

Update `IMPLEMENTATION_HANDOFF.md`, then report: prerequisite audit; Slice 06 status (`complete`, `incomplete`, or `verification-blocked`); stable IDs and orchestration/observation behavior completed; files changed; every test command and result; remaining stable ID or blocker; relevant uncommitted worktree state; confirmation that no commit was created; and the exact stable ID at which to resume.
