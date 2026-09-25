# Slice 02 implementation prompt: Job-template policy

Implement Slice 02 only. Leave the worktree reviewable and resumable; do not begin Slice 03 in this run.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns the cross-cutting contracts.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read [`../IMPLEMENTATION_HANDOFF.md`](../IMPLEMENTATION_HANDOFF.md) completely and validate it against the current worktree.
4. Read the target contract in [`../slices/02-job-template-policy.md`](../slices/02-job-template-policy.md) and the incoming deferred group `S01-D02` completely. Read Slice 01 completely only if its completion evidence is missing, this work changes its contract, or a failure points back to it.

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, relevant diffs, generated alpha4 types/CRD, and Slice 01 completion evidence. Verify that evidence against relevant code, generated output, and tests without automatically rereading or rerunning all prior work. Do not rely only on the handoff or the presence of alpha4 files. If Slice 01 is incomplete or verification-blocked, stop and report the first unmet stable ID; do not implement Slice 02. Otherwise update the handoff with the base commit, first incomplete Slice 02 ID, and `in-progress` state.

## Required outcome

Implement `S01-D02`, Slice 02 local milestones `S02-M1` through `S02-M4`, local acceptance groups `S02-A1` through `S02-A3`, and the Slice 02 completion-and-handoff criteria at the Operator-internal policy boundary. `S02-D03` and `S02-D06` define later integration work and do not block Slice 02. Preserve the default-deny, typed alpha4 allow-list fixed to Kubernetes 1.30 semantics; validate without mutating the input; collect precise field errors; normalize only after validation; and keep the validator independent of Kubernetes defaulting and the native `k8s.io/kubernetes` validator. Cover the complete allow-list, field census, security ownership, resource semantics, merge behavior, and accepted/rejected fixtures described by the slice rather than replacing them with a narrower sample.

Do not move validation into Scenario Manager, broaden the public surface based on newer build-time Go structs, or silently accept protected fields.

## Execution and verification rules

- Follow nearby patterns and keep changes within the slice's ownership boundary.
- Preserve user changes. Do not reset destructive state, expose secrets, or weaken tests. Do not create commits.
- Run focused validator and fixture tests during development, including input non-mutation and Kubernetes 1.30 compatibility cases.
- Before claiming completion, run repository-root `make test-fast` and the mandatory `make test-smoke` under the exact current `AGENTS.md` safety contract.
- If smoke prerequisites are unavailable, finish all safe implementation and fast checks, mark Slice 02 `verification-blocked`, and stop without beginning Slice 03.

## Final handoff

Update `IMPLEMENTATION_HANDOFF.md`, then report: prerequisite audit; Slice 02 status (`complete`, `incomplete`, or `verification-blocked`); stable IDs and policy groups completed; files changed; every test command and result; remaining stable ID or blocker; relevant uncommitted worktree state; confirmation that no commit was created; and the exact stable ID at which to resume.
