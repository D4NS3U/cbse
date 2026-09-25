# Slice 07 implementation prompt: Images, smoke, and documentation

Implement Slice 07 only. This is the final integration and alpha4 cutover slice. Leave the worktree reviewable and resumable.

## Read order and authority

1. First read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns every cross-cutting contract and the coordinated cutover checkpoint.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey all applicable instructions.
3. Read [`../IMPLEMENTATION_HANDOFF.md`](../IMPLEMENTATION_HANDOFF.md) completely and validate it against the current worktree.
4. Read the target contract in [`../slices/07-images-smoke-and-documentation.md`](../slices/07-images-smoke-and-documentation.md) and incoming deferred groups `S01-D07`, `S03-D07`, `S05-D07`, and `S06-D07` completely. The slice now begins with **S07-M0 — the repository fold** (consume [`../slices/06.5-alpha4-sm-composition.md`](../slices/06.5-alpha4-sm-composition.md)); read that slice and its `Repository fold obligation` section in Slice 07 completely. Read an earlier slice completely only if its completion evidence is missing, the cutover changes its contract, or a failure points back to it.

Treat the specification files as normative and read-only. Report an irreconcilable contradiction instead of changing them.

## Prerequisite and worktree gate

Before editing, inspect `git status --short`, all relevant diffs, generated artifacts, active schemes/imports, image and lock tooling, Kubernetes manifests and RBAC, smoke preflight/cleanup, compatibility lanes, and public documentation. Verify recorded completion evidence for Slices 01 through 06 **and Slice 06.5** against relevant code and tests without automatically rereading or rerunning all prior work. Do not rely only on the handoff. If any prior slice is incomplete or verification-blocked, stop at the earliest stable ID and do not begin the fold or cutover. Otherwise update the handoff with the base commit, first incomplete Slice 07 ID (`S07-M0`), and `in-progress` state.

**Slice 07 Stage 1 is already committed** (images/harness/docs/go.work translator/eds_intake at `033d88e`): the Makefile, `test/e2e/images.lock.env`, `test/harness/*` (image-lock, build-images, preflight, test-harness, registry-cleanup), `go.work` translator wiring, translator/Detail-DB READMEs, `docs/CBSE_TESTING_GUIDE.md`, and `COMPONENT_DESIGN_GOALS.md` are already in place. Do NOT redo that work. Build on it for the cutover (flip the build default from alpha3 `exop,sm,eds-mock,trans-mock` to the alpha4 6-component set, flip smoke manifests to alpha4, etc.).

## Phase ordering (mandatory)

Slice 07 is executed in two phases with a hard gate between them:

**Phase 1 — Repository fold (S07-M0), `make test-fast` gate.** Move every package under `scenario-manager/internal/alpha4/` to its final `internal/` home per the slice's `Repository fold obligation`, update all import paths, merge the composition (`RunScenarioManager`, informer, NATS adapters, selection loop, ready workflow) into `internal/core` and `internal/nats`, delete the dead alpha3 files in `internal/core`, `internal/nats`, `internal/coredb`, `internal/communication`, and `internal/subject`, and remove the `scenario-manager/internal/alpha4/` directory. `cmd/main.go` still imports `internal/core` after the fold (its import does not change in this phase). Run `make test-fast` and require **rc=0 before starting Phase 2**. The fold is a mechanical rename + an in-place merge of the composition + a deletion of dead code; it must not change any contract or test expectation. Do the fold FIRST and confirm `make test-fast` rc=0 before touching the CRD, `cmd/main.go`'s import, smoke manifests, or the build default.

**Phase 2 — Cutover + smoke (S07-M1 onward).** Only after Phase 1 is green: flip `cmd/main.go`'s import to the folded `internal/core.RunScenarioManager` (alpha4), flip the served/storage CRD scheme to alpha4 (assert alpha2/alpha3 not served), flip the build default to the real translator 6-component set, flip smoke manifests/RBAC/fixtures to alpha4, and run `make test-fast` then the mandatory `make test-smoke` with the deferred `S05-D07`/`S06-D07` real-translator e2e checks.

## Required outcome

Implement incoming groups `S01-D07`, `S03-D07`, `S05-D07`, and `S06-D07`, local milestones `S07-M0` through `S07-M5`, local acceptance groups `S07-A0` through `S07-A4`, and the Slice 07 completion-and-handoff criteria as one coordinated integration, in the phase order above. Switch active schemes, CRD serving/storage, fixtures, manifests, RBAC, compatibility coverage, and smoke assertions to alpha4 together; do not leave a mixed-version state. Integrate the exact locked source images and repository-built immutable outputs, reference Translator and Detail DB builds, minimum Kubernetes 1.30 lane and smoke preflight, approved namespace/RBAC behavior, protected credential handoff, reference end-to-end workflow, annotation- and tag-verified Harbor cleanup, and required user/developer documentation.

Never delete or migrate the shared CRD, weaken cluster or registry preflight, introduce floating tags or image overrides, expose credentials or credential paths, target shared image repositories during cleanup, add insecure-registry/TLS bypasses, or retain alpha2/alpha3 serving or reconciliation as compatibility behavior.

## Execution and verification rules

- Make cutover-related generated output, manifests, fixtures, tests, and documentation agree before declaring success.
- Preserve user changes. Do not reset destructive state or weaken tests. Do not create commits.
- Run focused lock, rendering, shell-harness, preflight, cleanup-adapter, compatibility, CRD, RBAC, documentation, and end-to-end checks during development.
- Before claiming completion, run repository-root `make test-fast`, then the mandatory full `make test-smoke` under the exact current `AGENTS.md` safety contract with explicit `KUBECONFIG`, the required immutable inputs, and protected registry authentication.
- If the approved cluster or protected runtime input is unavailable, finish all safe implementation and fast checks, mark Slice 07 `verification-blocked`, and stop. Do not describe the feature as fully accepted.

## Final handoff

Update `IMPLEMENTATION_HANDOFF.md`, then report: prerequisite audit; Slice 07 status (`complete`, `incomplete`, or `verification-blocked`); stable IDs and cutover/image/smoke/cleanup/documentation behavior completed; files changed; generated artifacts; every test command and result; remaining stable ID or external blocker; relevant uncommitted worktree state; confirmation that no commit was created; and either the exact stable ID at which to resume or `Resume at: all seven slices complete` when full mandatory acceptance passed.
