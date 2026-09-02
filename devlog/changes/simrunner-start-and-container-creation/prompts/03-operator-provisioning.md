# Slice 03 implementation prompt: Operator provisioning

Implement Slice 03 only. Leave the worktree reviewable and resumable; do not begin Slice 04 in this run.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns the cross-cutting contracts.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read Slices 01 through 03 completely, ending with the target contract in [`../slices/03-operator-provisioning.md`](../slices/03-operator-provisioning.md).

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, relevant diffs, the alpha4 API/CRD, the canonical Job-template validator, current controller reconciliation, database endpoint handling, and fixtures. Verify Slices 01 and 02 against their completion criteria and required test evidence. If either earlier slice is incomplete or verification-blocked, stop at the earliest such slice and do not implement Slice 03.

## Required outcome

Implement every Slice 03 requirement and acceptance criterion. Keep provisioning explicit and idempotent: validate admitted alpha4 configuration and registry credentials, use the Operator-internal Job-template policy as the sole policy boundary, implement the common database endpoint and availability-probe contract, and reconcile the specified database, Translator, BuildKit, Secret, Service, volume, security, ownership, and runner-ServiceAccount resources. Preserve the separation between availability probes and application database work, and reach `InProgress` only after all required validation and readiness gates succeed.

Do not add in-place mutation of immutable Translator or database configuration, credential rotation, insecure registry behavior, or Scenario Manager-side template validation.

## Execution and verification rules

- Follow existing controller patterns and verify repeated reconciliation and partial-resource recovery.
- Preserve user changes. Do not reset destructive state, expose secrets, or weaken tests. Do not create commits.
- Run focused controller, endpoint, Secret, security-context, resource-shape, and envtest checks during development.
- Before claiming completion, run repository-root `make test-fast` and the mandatory `make test-smoke` under the exact current `AGENTS.md` safety contract.
- If smoke prerequisites are unavailable, finish all safe implementation and fast checks, mark Slice 03 `verification-blocked`, and stop without beginning Slice 04.

## Final handoff

Report: prerequisite audit; Slice 03 status (`complete`, `incomplete`, or `verification-blocked`); resources and failure paths implemented; files changed; every test command and result; remaining acceptance criteria or blockers; relevant uncommitted worktree state; confirmation that no commit was created; and `Resume at: Slice 03` unless the full slice and mandatory verification passed, in which case use `Resume at: Slice 04`.
