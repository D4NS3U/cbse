# Pause checkpoint — Helm control-plane installation

**Recorded:** 2026-11-04  
**Feature branch:** `D4NS3U/helm-control-plane-installation`  
**Implementation checkpoint:** `319893592eab4ab784859b5c613489713d6d478d`

This checkpoint records the feature state before returning the repository-root checkout to `main`. The feature has **not** been merged into `main`; final landing remains user-approved work after H5, H6, and final settlement.

## Slice state

| Slice | State | Feature-branch landing |
|---|---|---|
| H1 — chart skeleton and contract tests | complete, independently approved | `bcc6d87` |
| H2 — Operator and Scenario Manager workloads | complete, independently approved | `6969513` |
| H3 — internal NATS and Core DB | complete, independently approved | `e465775` |
| H4 — lifecycle safety | complete, independently approved | `b453fff` |
| H5 — Helm smoke integration | incomplete; unblocked and not dispatched | — |
| H6 — documentation | incomplete; waits for H5 | — |

The detailed task/dispatch IDs, model attestations, amendments, findings, and evidence receipts remain in [IMPLEMENTATION_HANDOFF.md](IMPLEMENTATION_HANDOFF.md).

## Proven state

- H1–H4 static/chart/fast gates are green.
- H4 lifecycle acceptance was independently rerun successfully as `20261104093529-9ce91c`.
- H4 manifest smoke was independently rerun successfully as `20261104093746-238904`: **15/15 passed**, rc=0.
- Harness teardown was verified: run namespaces absent, smoke Lease absent, zero remaining `SimulationExperiment` resources, shared CRD retained.
- `cbse-registry-auth` remains installation-owner-only; Helm only validates and references it.
- `core-db-credentials` and persistent data use the ruled Helm retention lifecycle.
- The pre-delete guard blocks uninstall while experiments remain and never deletes experiments.
- `clean.sh` now deletes experiment CRs and waits for finalizers before namespace deletion.

## Remaining work

### H5 — Helm smoke integration

H5 must:

1. add `manifest|helm` installer selection to the same unchanged 15-spec suite;
2. consume the H4 private image-values generator and lifecycle contracts rather than replacing them;
3. prove both manifest and Helm modes at **15/15**;
4. provide live Helm-installed proof for NATS `readOnlyRootFilesystem` and Core DB `PGDATA` persistence behavior;
5. preserve immutable digest images, explicit kubeconfig/context checks, harness namespace ownership, and Secret non-disclosure;
6. receive independent review and land only on the feature branch.

### H6 — documentation

H6 remains blocked on H5 and must document only behavior proven by the settled implementation and acceptance evidence.

## Resume procedure

1. Switch to `D4NS3U/helm-control-plane-installation` and confirm a clean tree.
2. Re-read `FEATURE.md`, `IMPLEMENTATION_HANDOFF.md`, this checkpoint, and the H5 slice/prompt.
3. Create a dedicated H5 Orca child worktree from the latest feature-branch HEAD.
4. Use `ai.forge/qwen3.8-27b-nvfp4` for implementation and independent `ai.forge/glm` review.
5. Land approved work only into the feature branch. Do not merge into `main` before final settlement and explicit user approval.

## Operational evidence

Implementation, review, lifecycle, smoke, and image evidence remains below ignored `artifacts/` directories in the local checkout. Those directories are operational evidence and must never be committed. No credentials, Secret payloads, private repository values, cluster endpoints, or kubeconfig paths belong in tracked handoff documents.
