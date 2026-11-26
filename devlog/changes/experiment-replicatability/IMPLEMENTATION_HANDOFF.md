# Implementation handoff — Deterministic replication streams

This is the durable routing ledger for [FEATURE.md](FEATURE.md). Slice states use
`complete`, `incomplete`, or `verification-blocked`. Specifications are
normative and read-only to implementation/review workers.

## Current state

**Package status:** draft implementation package; no orchestration Run, Task, or
Dispatch exists. User approval of the pinned contracts and slice DAG is required
before W1 dispatch because the package changes the alpha4 API/CRD and generated
runner/result contract.

| Slice | Wave | Dependencies | State | Landing / evidence | Next owner |
|---|---:|---|---|---|---|
| D1 — root-seed API and scenario-seed contract | W1 | none | **incomplete** | specification only | implementation worker after user approval |
| D3 — runner-base derivation Facade | W1 | none | **incomplete** | specification only | implementation worker after user approval |
| D2 — Scenario Manager replication coordinates | W2 | D1 | **incomplete** | specification only | blocked on D1 |
| D4 — reference runner and idempotent results | W3 | D2, D3 | **incomplete** | specification only | blocked on D2/D3 |
| D5 — dual-installer reproducibility acceptance | W4 | D4 | **incomplete** | specification only | blocked on D4 |
| D6 — reproducibility documentation | W5 | D5 | **incomplete** | specification only | blocked on D5 |

## Pinned draft contracts awaiting approval

1. Required immutable `spec.randomness.rootSeed`, range `0..MaxInt64`, no
   generated mode.
2. Existing `seed_policy` is the scenario seed; no new schema field; reuse is
   allowed for explicit common-random-number coupling across scenarios.
3. Global replication index is cumulative completed count before the wave plus
   Kubernetes completion index.
4. Runtime variables are `CBSE_ROOT_SEED`, `CBSE_REPLICATION_OFFSET`, and
   `CBSE_COMPLETION_INDEX`.
5. Base-image derivation is `cbse-hmac-sha256-u63-v1` with the exact bytes and
   golden vectors in FEATURE.md.
6. Result identity is scenario table plus global replication index.
7. Identical same-index retries are idempotent; conflicting retries fail closed.
8. Legacy append-only result tables are rejected, not migrated by runner Pods.
9. Final proof uses one immutable image set through manifest and Helm smoke and
   compares normalized seed manifests across different experiment recreations.

## Planned orchestration DAG

```text
W1: D1 ─────┐
             ├─> W2: D2 ──┐
W1: D3 ────────────────────┴─> W3: D4 -> W4: D5 -> W5: D6
```

D1 and D3 have disjoint targets and may dispatch together. Every implementation
slice receives an independent `ai.forge/glm` review on its exact HEAD before
feature-branch landing. Review fixes return to the same worktree and receive a
new review round. Three review rounds on one slice is the escalation limit.

## Branch and landing policy

- Feature branch: `D4NS3U/experiment-replicatability`.
- Implementation workers use dedicated Orca child worktrees.
- Approved slice branches land only on the feature branch.
- API/CRD and `devlog/**` are protected paths; merge gates require the explicit
  approval captured for this package.
- No merge to local `main` without a separate explicit user instruction.
- No push to `origin/main` without another separate instruction.

## Test policy summary

| Slice | Fast | Manifest smoke | Helm smoke | Special evidence |
|---|---|---|---|---|
| D1 | required | required | no | CRD/chart byte identity |
| D2 | required | required | no | exact Job runtime variables |
| D3 | required | required | no | base image digest + golden vectors |
| D4 | required | required | no | provenance + idempotent result rows |
| D5 | required | required | required | same-image cross-run seed-manifest equality |
| D6 | required | no | no | links/content/hygiene |

All cluster commands require explicit `KUBECONFIG` and
`CBSE_EXPECTED_CONTEXT=k3s-test`. A required unavailable cluster gate is
`verification-blocked`.

## Resume procedure

1. Confirm branch `D4NS3U/experiment-replicatability` is clean and contains the
   approved devlog package.
2. Confirm user approval of the nine pinned contracts above.
3. Commit the manager-authored specification package if requested/appropriate;
   do not dispatch workers against uncommitted ambiguous specs.
4. Create one Orca Run for the feature.
5. Create D1 and D3 Tasks with their complete slice text and start both W1 pi
   workers using explicit model argv `ai.forge/qwen3.8-27b-nvfp4`.
6. Require first-checkpoint model attestation and orchestration commit trailers.
7. Independently review each completed W1 branch with `ai.forge/glm`.
8. Land approved W1 work on the feature branch, update this ledger, then release
   settled terminals/worktrees before opening D2.

## Manager-only final settlement

After D6 approval, the manager updates:

- `THESIS_EXPLANATION.md` to the exact landed implementation/evidence;
- this ledger with all Task/Dispatch IDs, commits, reviews, smoke run IDs, image
  digests, and comparison receipt;
- `devlog/changes/README.md` status.

Then run landed-tree `make test-fast` and, because the feature changes API,
reconciliation, images, and integration, one final dual-installer settlement
pair if the D5 evidence does not already cover the exact final code HEAD.
