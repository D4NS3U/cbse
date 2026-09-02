# Slice 02 implementation prompt: Job-template policy

Implement Slice 02 only. Leave the worktree reviewable and resumable; do not begin Slice 03 in this run.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns the cross-cutting contracts.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read Slice 01 and Slice 02 completely, ending with the target contract in [`../slices/02-job-template-policy.md`](../slices/02-job-template-policy.md).

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, relevant diffs, generated alpha4 types/CRD, and Slice 01 tests. Verify Slice 01 against its requirements, acceptance criteria, generated-output state, and required test evidence. Do not rely only on a prior handoff or the presence of alpha4 files. If Slice 01 is incomplete or verification-blocked, stop and report the first unmet prerequisite; do not implement Slice 02.

## Required outcome

Implement every Slice 02 requirement and acceptance criterion at the Operator-internal policy boundary. Preserve the default-deny, typed alpha4 allow-list fixed to Kubernetes 1.30 semantics; validate without mutating the input; collect precise field errors; normalize only after validation; and keep the validator independent of Kubernetes defaulting and the native `k8s.io/kubernetes` validator. Cover the complete allow-list, field census, security ownership, resource semantics, merge behavior, and accepted/rejected fixtures described by the slice rather than replacing them with a narrower sample.

Do not move validation into Scenario Manager, broaden the public surface based on newer build-time Go structs, or silently accept protected fields.

## Execution and verification rules

- Follow nearby patterns and keep changes within the slice's ownership boundary.
- Preserve user changes. Do not reset destructive state, expose secrets, or weaken tests. Do not create commits.
- Run focused validator and fixture tests during development, including input non-mutation and Kubernetes 1.30 compatibility cases.
- Before claiming completion, run repository-root `make test-fast` and the mandatory `make test-smoke` under the exact current `AGENTS.md` safety contract.
- If smoke prerequisites are unavailable, finish all safe implementation and fast checks, mark Slice 02 `verification-blocked`, and stop without beginning Slice 03.

## Final handoff

Report: prerequisite audit; Slice 02 status (`complete`, `incomplete`, or `verification-blocked`); implemented policy groups; files changed; every test command and result; remaining matrix rows or blockers; relevant uncommitted worktree state; confirmation that no commit was created; and `Resume at: Slice 02` unless the full slice and mandatory verification passed, in which case use `Resume at: Slice 03`.
