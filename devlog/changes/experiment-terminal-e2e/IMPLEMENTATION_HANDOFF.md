# IMPLEMENTATION_HANDOFF — experiment-terminal-e2e

**Status: implemented — all three slices complete, reviewed, and cluster-proven (11/11 live). Merge to `main` routed to the user (the single settlement merge of branch `D4NS3U/experiment-terminal-e2e`).**

The experiment's terminal branches are now live-proven on the cluster: **three `Error` flavors** (the Validation-Error gate — zero children; the Provisioning-Error NodePort conflict — partial children persist + GC cascade; the readiness watchdog's image-pull failure — the 3-retry budget with the per-retry aggregated message) and the **`Failed` chain** (the deadline-exceeded runner Job → `ObservationFailed` → the guarded `InProcessing → Failed` → the fail-fast verdict → the derived phase + Event + stickiness + the terminal action's job cleanup).

## Slices (all `complete`)

| Slice | Branch commits | Lanes | Review verdict |
|---|---|---|---|
| [T1 — the Error flavors spec](slices/T1-error-flavors-spec.md) | `7cac516` | `task_f642f9edcc92` / `ctx_c496c5570ebd` → review `task_11956b120c0d` / `ctx_d88fe9124be1` | **approve** |
| [T2 — the readiness watchdog](slices/T2-readiness-watchdog.md) | `50bc0f5` | `task_b2748273b7ac` / `ctx_ead6261c54a9` → review `task_41c7206f8826` / `ctx_a844caa23d0a` | **approve** |
| [T3 — the F-crash chain](slices/T3-fcrash-chain-spec.md) | `3a25de6` + `5524e4e` + `a962c4e` + `1d2c8ee` | `task_2a303d0c9e60` / `ctx_40d8b7540c9b` (one stop-and-ask: the result-sink trigger's empirical invalidation) → review `task_1ce78f8cbe6e` / `ctx_878affc8dac7` | **approve** |

Every implementer attested `ai.forge/qwen3.8-27b-nvfp4`; every reviewer attested `ai.forge/glm`. All dispatches carried the orchestration trailer; workers never touched `main`.

## The settlement story (the gate earned its keep — three catches)

1. **A production defect**: the operator's PhaseTransition Event was silently Forbidden on real clusters (no `events` RBAC — masked by envtest's permissive mode in every review lane). Fixed (`5524e4e`) — the P3-era D7 feature actually works now.
2. **A harness gap**: the suite outgrew the go test default timeout (10m < the red+green matrix); the harness gains `-timeout 40m` (`a962c4e`).
3. **A wrong test premise**: the triage expected the runner Job's `JobFailed` condition to persist, but the SM's terminal action removes Failed experiments' Jobs by design; the assertion now proves that cleanup behavior instead (`1d2c8ee`).

Plus one mid-implementation design correction: the F-crash trigger's result-sink form was **empirically disproven by the implementer** (the runner self-provisions its result table) and re-ruled to the job-deadline delta — all recorded in the design note.

## Verification

- Per-slice tier gates: `make test-fast` rc=0, independently re-run by each reviewer.
- **The consolidated settlement gate: 11/11 specs green** (run `20261001124216-a59a4a`, SKIP_BUILD with the settlement digests; the registry's CrowdSec block hit the runner-base push mid-run — the four rebuilt images including the watchdog operator pushed before it; the cluster pulls via its own path). The live artifacts: the red experiments' terminal statuses, the scenario rows, the events, and the F-crash chain's `Failed` capture (`phase=Failed`, `scenarioManagerVerdict=Failed`, all four scenarios `Failed`, the jobs removed by the terminal action).
- The cluster is clean (no leaked namespaces).

## Merge decision — for the user

Branch `D4NS3U/experiment-terminal-e2e` (tip `1d2c8ee`) holds the complete reviewed feature. On your approval, the manager applies the merge gate and lands it on `main` as one reviewed merge; the worktree is removed afterwards.

## Image digests (the settlement set)

Operator (with the watchdog): `exop@sha256:45110c29…`; SM: `sm@sha256:2be98c827…`; eds-mock: `eds-mock@sha256:a94f43b5…`; translator: `translator@sha256:63f0869d…`; the unchanged runner-base/detail-db/pps from the prior settlement. Full env in the run's `images.env`.
