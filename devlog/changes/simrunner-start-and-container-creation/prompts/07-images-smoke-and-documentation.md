# Slice 07 implementation prompt: Images, smoke, and documentation

Implement Slice 07 only. This is the final integration and alpha4 cutover slice. Leave the worktree reviewable and resumable.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns every cross-cutting contract and the coordinated cutover checkpoint.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read [`../IMPLEMENTATION_HANDOFF.md`](../IMPLEMENTATION_HANDOFF.md) completely and validate it against the current worktree.
4. Read the target contract in [`../slices/07-images-smoke-and-documentation.md`](../slices/07-images-smoke-and-documentation.md) and incoming deferred groups `S01-D07`, `S03-D07`, `S05-D07`, and `S06-D07` completely. Read an earlier slice completely only if its completion evidence is missing, the cutover changes its contract, or a failure points back to it.

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, all relevant diffs, generated artifacts, active schemes/imports, image and lock tooling, Kubernetes manifests and RBAC, smoke preflight/cleanup, compatibility lanes, and public documentation. Verify recorded completion evidence for Slices 01 through 06 in numeric order against relevant code and tests without automatically rereading or rerunning all prior work. Do not rely only on the handoff. If any prior slice is incomplete or verification-blocked, stop at the earliest stable ID and do not begin the cutover. Otherwise update the handoff with the base commit, first incomplete Slice 07 ID, and `in-progress` state.

## Required outcome

Implement incoming groups `S01-D07`, `S03-D07`, `S05-D07`, and `S06-D07`, local milestones `S07-M1` through `S07-M5`, local acceptance groups `S07-A1` through `S07-A4`, and the Slice 07 completion-and-handoff criteria as one coordinated integration. Switch active schemes, CRD serving/storage, fixtures, manifests, RBAC, compatibility coverage, and smoke assertions to alpha4 together; do not leave a mixed-version state. Integrate the exact locked source images and repository-built immutable outputs, reference Translator and Detail DB builds, minimum Kubernetes 1.30 lane and smoke preflight, approved namespace/RBAC behavior, protected credential handoff, reference end-to-end workflow, annotation- and tag-verified Harbor cleanup, and required user/developer documentation.

Never delete or migrate the shared CRD, weaken cluster or registry preflight, introduce floating tags or image overrides, expose credentials or credential paths, target shared image repositories during cleanup, add insecure-registry/TLS bypasses, or retain alpha2/alpha3 serving or reconciliation as compatibility behavior.

## Execution and verification rules

- Make cutover-related generated output, manifests, fixtures, tests, and documentation agree before declaring success.
- Preserve user changes. Do not reset destructive state or weaken tests. Do not create commits.
- Run focused lock, rendering, shell-harness, preflight, cleanup-adapter, compatibility, CRD, RBAC, documentation, and end-to-end checks during development.
- Before claiming completion, run repository-root `make test-fast`, then the mandatory full `make test-smoke` under the exact current `AGENTS.md` safety contract with explicit `KUBECONFIG`, the required immutable inputs, and protected registry authentication.
- If the approved cluster or protected runtime input is unavailable, finish all safe implementation and fast checks, mark Slice 07 `verification-blocked`, and stop. Do not describe the feature as fully accepted.

## Final handoff

Update `IMPLEMENTATION_HANDOFF.md`, then report: prerequisite audit; Slice 07 status (`complete`, `incomplete`, or `verification-blocked`); stable IDs and cutover/image/smoke/cleanup/documentation behavior completed; files changed; generated artifacts; every test command and result; remaining stable ID or external blocker; relevant uncommitted worktree state; confirmation that no commit was created; and either the exact stable ID at which to resume or `Resume at: all seven slices complete` when full mandatory acceptance passed.
