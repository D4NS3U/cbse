# Slice 07 implementation prompt: Images, smoke, and documentation

Implement Slice 07 only. This is the final integration and alpha4 cutover slice. Leave the worktree reviewable and resumable.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns every cross-cutting contract and the coordinated cutover checkpoint.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read all seven slice documents completely, ending with the target contract in [`../slices/07-images-smoke-and-documentation.md`](../slices/07-images-smoke-and-documentation.md).

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, all relevant diffs, generated artifacts, active schemes/imports, image and lock tooling, Kubernetes manifests and RBAC, smoke preflight/cleanup, compatibility lanes, and public documentation. Verify Slices 01 through 06 in numeric order against every completion criterion and required test tier. Do not rely only on an earlier handoff. If any prior slice is incomplete or verification-blocked, stop at the earliest such slice and do not begin the cutover.

## Required outcome

Implement every Slice 07 requirement and acceptance criterion as one coordinated integration. Switch active schemes, CRD serving/storage, fixtures, manifests, RBAC, compatibility coverage, and smoke assertions to alpha4 together; do not leave a mixed-version state. Integrate the exact locked source images and repository-built immutable outputs, reference Translator and Detail DB builds, minimum Kubernetes 1.30 lane and smoke preflight, approved namespace/RBAC behavior, protected credential handoff, reference end-to-end workflow, annotation- and tag-verified Harbor cleanup, and required user/developer documentation.

Never delete or migrate the shared CRD, weaken cluster or registry preflight, introduce floating tags or image overrides, expose credentials or credential paths, target shared image repositories during cleanup, add insecure-registry/TLS bypasses, or retain alpha2/alpha3 serving or reconciliation as compatibility behavior.

## Execution and verification rules

- Make cutover-related generated output, manifests, fixtures, tests, and documentation agree before declaring success.
- Preserve user changes. Do not reset destructive state or weaken tests. Do not create commits.
- Run focused lock, rendering, shell-harness, preflight, cleanup-adapter, compatibility, CRD, RBAC, documentation, and end-to-end checks during development.
- Before claiming completion, run repository-root `make test-fast`, then the mandatory full `make test-smoke` under the exact current `AGENTS.md` safety contract with explicit `KUBECONFIG`, the required immutable inputs, and protected registry authentication.
- If the approved cluster or protected runtime input is unavailable, finish all safe implementation and fast checks, mark Slice 07 `verification-blocked`, and stop. Do not describe the feature as fully accepted.

## Final handoff

Report: prerequisite audit; Slice 07 status (`complete`, `incomplete`, or `verification-blocked`); cutover, image, smoke, cleanup, and documentation behavior implemented; files changed; generated artifacts; every test command and result; remaining acceptance criteria or external blockers; relevant uncommitted worktree state; confirmation that no commit was created; and either `Resume at: Slice 07` with the first remaining task or `Resume at: all seven slices complete` when full mandatory acceptance passed.
