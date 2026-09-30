# IMPLEMENTATION_HANDOFF — experiment-phase-aggregation

**Status: implemented — all four slices complete, reviewed, and cluster-proven. Merge to `main` routed to the user (the single settlement merge of branch `D4NS3U/experiment-phase-aggregation`).**

The `SimulationExperiment` CR gained its terminal phases via the field-level-ownership methodology of [the design note](notes/component-reports-and-field-ownership.md): the Scenario Manager owns and reports `status.scenarioManagerVerdict` (`Finished | Failed`, a plain absorbing string enum — the scenario-aggregate ground truth from its Core Database); the Experiment Operator reads the report and exclusively derives `status.phase` (`Finished` when all scenarios finished, `Failed` when any failed — fail-fast), with terminal stickiness inherited from the existing park architecture and a Normal Event on the transition.

## Slices (all `complete`)

| Slice | Branch commit | Lanes | Review verdict |
|---|---|---|---|
| [P1 — API surface](slices/P1-api-surface.md) | `ceedfbb` | `task_c135b5893dd6` / `ctx_7144a7b07c0d` → review `task_ae96fa1c635a` / `ctx_34ed81bcf947` | **approve** |
| [P2 — SM aggregation pass](slices/P2-sm-aggregation-pass.md) | `457cc13` + fix `a381b4f` | `task_2ec081a369cf` / `ctx_9d0443e6face` → review `task_faa058c8d3b8` / `ctx_fb443a4ab341` (**request-changes**: the main-resource write) → fixes `task_ea0b3feeea55` / `ctx_8d5839291394` → review `task_8e3646cffa1f` / `ctx_645a10c1bf23` | **approve** (round 2) |
| [P3 — Operator derive](slices/P3-operator-derive.md) | `e1f6000` | `task_f5581981eb14` / `ctx_b59b214e0734` → review `task_8ff639f8ca94` / `ctx_0b2a23a68345` | **approve** |
| [P4 — e2e spec](slices/P4-e2e-spec.md) | `02c15cb` | `task_cb698665311a` / `ctx_bbd30e68d10c` (died: provider 400 pre-work) → replacement `ctx_b9f0f816aa13` → review `task_1d6e0e2bff2c` / `ctx_dd14825fd7c5` | **approve** |

Every implementer attested `ai.forge/qwen3.8-27b-nvfp4`; every reviewer attested `ai.forge/glm` (lane-separated error distributions). All dispatches carried the `[orchestration: task … dispatch …]` trailer; workers committed only to the branch; `main` was never touched by workers.

## Verification

- Per-slice tier gates: `make test-fast` rc=0 (re-run independently by each reviewer).
- **Consolidated settlement gate:** `make test-smoke` (run `20260930154245-59c483`, full image build + push): **7/7 specs green** on the cluster, including the new live Finished-chain spec. The harness removed its namespace; the cluster is clean.
- The review lane caught one live-cluster-critical defect (P2 round 1: the verdict write targeted the main resource instead of the status subresource — silently discarded by the API server; envtest-proven) and it was fixed and re-approved. One dispatch died on a provider 400 pre-work and was replaced per the kill protocol.

## Merge decision — for the user

The branch `D4NS3U/experiment-phase-aggregation` (worktree `experiment-phase-aggregation`, tip `02c15cb`) holds the complete reviewed feature: 6 commits of implementation (P1, P2, P2-fix, P3, P4, plus docs merges). Per the branch-isolation ruling and the protected paths in the diff (the regenerated CRD manifest; the stack.yaml Role), the merge to `main` is a single user-approved event. On approval, the manager applies MANAGER.md's merge gate (approve receipts at the reported HEADs, green recipes, scope match, hygiene) and lands the branch on local `main`; the worktree is removed afterwards.
