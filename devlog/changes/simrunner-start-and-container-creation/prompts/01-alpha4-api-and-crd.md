# Slice 01 implementation prompt: Alpha4 API and CRD

Implement Slice 01 only. Leave the worktree reviewable and resumable; do not begin Slice 02 in this run.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns the cross-cutting contracts.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read [`../slices/01-alpha4-api-and-crd.md`](../slices/01-alpha4-api-and-crd.md) completely, including its acceptance tests, exclusions, test tier, and handoff criteria.

Treat the feature and slice documents as normative and read-only. Report an irreconcilable contradiction instead of changing the specification.

## Prerequisite and worktree gate

Slice 01 has no feature-slice prerequisite. Before editing, inspect `git status --short`, relevant diffs, current alpha2/alpha3 API patterns, generated-code tooling, CRD manifests, schemes, samples, and tests. Preserve all pre-existing work and continue compatible partial Slice 01 changes rather than overwriting them.

## Required outcome

Implement every Slice 01 requirement and acceptance criterion. In particular, keep this slice an additive alpha4 API/CRD foundation: create the typed alpha4 surface, validation and immutability rules, generated code/manifests, samples, and tests owned by the slice, while leaving the coordinated repository-wide serving/storage/import/fixture cutover to Slice 07. Do not prematurely perform the alpha4-only active cutover, retain a compatibility reconciler, or add migration/conversion behavior.

Keep credentials outside the API, keep the Job-template subtree structural, and keep generated artifacts reproducible and current. Do not weaken validation tests or hand-edit generated output when the repository's generator is the authority.

## Execution and verification rules

- Follow nearby implementation patterns; avoid unrelated refactors and out-of-scope features.
- Preserve user changes. Do not reset destructive state or expose secrets. Do not create commits.
- Run focused API, CRD, admission, schema, and generated-output checks while developing.
- Before claiming completion, run repository-root `make test-fast` and the mandatory `make test-smoke` under the exact current `AGENTS.md` safety contract.
- If protected smoke inputs or the approved cluster are unavailable, finish all safe implementation and fast checks, mark Slice 01 `verification-blocked`, and stop. Do not call it complete or begin Slice 02.

## Final handoff

Report: Slice 01 status (`complete`, `incomplete`, or `verification-blocked`); implemented behavior; files changed; generated artifacts; every test command and result; remaining acceptance criteria or blockers; relevant uncommitted worktree state; confirmation that no commit was created; and `Resume at: Slice 01` unless the full slice and mandatory verification passed, in which case use `Resume at: Slice 02`.
