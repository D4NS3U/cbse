# Slice 03 implementation prompt: Operator provisioning

Implement Slice 03 only. Leave the worktree reviewable and resumable; do not begin Slice 04 in this run.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns the cross-cutting contracts.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read [`../IMPLEMENTATION_HANDOFF.md`](../IMPLEMENTATION_HANDOFF.md) completely and validate it against the current worktree.
4. Read the target contract in [`../slices/03-operator-provisioning.md`](../slices/03-operator-provisioning.md) and incoming deferred groups `S01-D03` and `S02-D03` completely. Read an earlier slice completely only if its completion evidence is missing, this work changes its contract, or a failure points back to it.

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, relevant diffs, the alpha4 API/CRD, the canonical Job-template validator, current controller reconciliation, database endpoint handling, and fixtures. Verify the recorded completion evidence for Slices 01 and 02 against relevant code and tests without automatically rereading or rerunning all prior work. If either earlier slice is incomplete or verification-blocked, stop at the earliest stable ID and do not implement Slice 03. Otherwise update the handoff with the base commit, first incomplete Slice 03 ID, and `in-progress` state.

## Required outcome

Implement incoming groups `S01-D03` and `S02-D03`, local milestones `S03-M1` and `S03-M2`, local acceptance group `S03-A1`, and the Slice 03 completion-and-handoff criteria. `S03-D07` defines later smoke and documentation work and does not block Slice 03. Keep provisioning explicit and idempotent: validate admitted alpha4 configuration and registry credentials, use the Operator-internal Job-template policy as the sole policy boundary, implement the common database endpoint and availability-probe contract, and reconcile the specified database, Translator, BuildKit, Secret, Service, volume, security, ownership, and runner-ServiceAccount resources. Preserve the separation between availability probes and application database work, and reach `InProgress` only after all required validation and readiness gates succeed.

Do not add in-place mutation of immutable Translator or database configuration, credential rotation, insecure registry behavior, or Scenario Manager-side template validation.

## Execution and verification rules

- Follow existing controller patterns and verify repeated reconciliation and partial-resource recovery.
- Preserve user changes. Do not reset destructive state, expose secrets, or weaken tests. Do not create commits.
- Run focused controller, endpoint, Secret, security-context, resource-shape, and envtest checks during development.
- Before claiming completion, run repository-root `make test-fast` and the mandatory `make test-smoke` under the exact current `AGENTS.md` safety contract.
- If smoke prerequisites are unavailable, finish all safe implementation and fast checks, mark Slice 03 `verification-blocked`, and stop without beginning Slice 04.

## Final handoff

Update `IMPLEMENTATION_HANDOFF.md`, then report: prerequisite audit; Slice 03 status (`complete`, `incomplete`, or `verification-blocked`); stable IDs, resources, and failure paths completed; files changed; every test command and result; remaining stable ID or blocker; relevant uncommitted worktree state; confirmation that no commit was created; and the exact stable ID at which to resume.
