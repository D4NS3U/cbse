# PADS paper implementation handoff

## Current state

Feature branch: `PADS-paper`

Current landed implementation: P3B2 at `8d79b3baceed1402346db7cfe4487bd40efc8160`

Next slice: P4 — Translator synchronization and component integration

`main` remains untouched. Paper-specific implementation ownership is rooted at `pads-paper/`; the shared `component-templates/**` tree remains the reusable baseline and must not be modified for the paper model.

## Slice ledger

| Slice | State | Implementation evidence | Independent review | Landing |
|---|---|---|---|---|
| P1 — model selection and initial-results documentation | complete | `0af44db` plus literature expansion `a1a8c11` | Literature worker metadata/source verification; documentation checks passed | landed on `PADS-paper` |
| P2 — paper component workspace foundation | complete | Task `task_ceac1745e6b4`, dispatch `ctx_e3b56683b6f0`; Pi runtime `ai.forge/qwen3.8-27b-nvfp4`; child HEAD `1d5ad7756f07a0a5c69f5fe6b2187e1d5b296cff`; exact eight README-only files; `git diff --check` and `make test-fast` passed | Task `task_78837d09ec33`, dispatch `ctx_245da2d5ddb3`; verdict `approve`; Pi runtime `ai.forge/glm`; reviewer reran exact-tree, baseline/ruling, hygiene, link, diff, and `make test-fast` checks (77 packages, zero failures) | fast-forwarded to `PADS-paper` at `1d5ad77` |
| P3A — exact template import | complete | Task `task_a4b168262c22`, dispatch `ctx_46adbe291a1f`; Pi/Qwen; initial HEAD `c78df24`; exact 110-file import parity plus provenance; `make test-fast` passed. Fix task `task_841ea2af0862`, dispatch `ctx_cbab86efea60`, corrected one provenance verification command at final HEAD `2b3fdb44bb80164cb480b27ce923ebddb195bbee` | Review round 1 task `task_030f3d367196`, dispatch `ctx_aba0590a6a64`: `request-changes` on one `find` assertion. Review round 2 task `task_04bd424a38b5`, dispatch `ctx_3b657f90bba3`: `approve`; Pi/GLM; parity and `make test-fast` rerun green | fast-forwarded to `PADS-paper` at `2b3fdb4` |
| P3B1 — canonical `erlang_a_v1` model and CLI | complete | Task `task_99236021dc1d`, dispatch `ctx_30d3b5fd5338`; Pi/Qwen; child HEAD `dbadcd8b7352d237bce31e3edee0cd387269c008`; original Erlang-A event logic, strict `build_result`, credential-free CLI, 52 tests OK; byte-identical CLI replay, invalid-input checks, no-global-random/no-credential AST checks, `git diff --check`, and `make test-fast` passed. Two mid-task pauses were steered back to work (long reasoning turn truncated; no code issue) | Task `task_848fff23916b`, dispatch `ctx_88a41cbc32b4`; verdict `approve`; Pi/GLM; reviewer audited every P3B1-R1..R8 requirement, probed simpy 4.1.2 internals including both tie orderings and the ungranted-request case, reran 52/52 tests, CLI replay, six invalid invocations, AST checks, `git diff --check`, and `make test-fast` (77 packages). One transient `ai.forge/glm` provider outage was resumed on the same model without incident | fast-forwarded to `PADS-paper` at `dbadcd8` |
| P3B2 — standalone model container | complete | Task `task_ec848cc8102b`, dispatch `ctx_6ccb950184f2`; Pi/Qwen; child HEAD `8d79b3baceed1402346db7cfe4487bd40efc8160`; locked two-stage chain (amd64) built locally; deterministic in-container replay, invalid-input, UID/GID 1000, exact `/runner` content, byte-identity of copied modules; `git diff --check` and `make test-fast` passed; nothing pushed to any registry | Task `task_8ff4a881611c`, dispatch `ctx_53ec3703ffc2`; verdict `approve`; Pi/GLM; reviewer rebuilt the chain from locked inputs and reran all in-container checks, `git diff --check`, and `make test-fast`. **Settlement smoke (coordinator-run, `SKIP_BUILD=1` over the published 26.11.4 digest set): run `20261105122418-e0acf4`, 15/15 specs passed** (an earlier coordinator attempt that re-pushed the existing `26.11.4` tags was rejected by the registry with 403; no cluster mutation had occurred and the harness left no residue) | fast-forwarded to `PADS-paper` at `8d79b3b` after the 15/15 smoke receipt |
| P4 — Translator synchronization and component integration | incomplete | not dispatched | not dispatched | not landed |
| P5 — end-to-end acceptance | incomplete | not dispatched | not dispatched | not landed |
| P6 — paper-result bundle | incomplete | not dispatched | not dispatched | not landed |

## Settled P2 contracts

- `pads-paper/` is the exclusive ownership root for paper-specific implementations.
- The EDS, Translator, PPS, and Scenario Detail Database must start from their existing `component-templates/**` implementations and receive only Erlang-A-required extensions.
- The simulation model starts from the Translator `runnermod` contract; its container starts from `runner-base` and the generated-runner Dockerfile contract.
- The Result Database preserves the existing official-PostgreSQL/no-repository-init behavior because no standalone repository Result DB template exists.
- Shared Experiment Operator, Scenario Manager, alpha4 CRD, and root test harness remain unchanged unless the user explicitly approves a later contradiction ruling.
- P2 is documentation-only; no executable component or completed-model claim exists yet.
- User-approved P3 ruling: `pads-paper/simulation-model/src/` is canonical and the Erlang-A implementation is original CBSE code, not copied from a GitHub model.
- User-approved P3 ruling: the Translator receives a synchronized model copy with automated parity, then uses its existing BuildKit sidecar to build and push a scenario-specific executable runner image.
- User-approved P3 ruling: the standalone model container is credential-free, accepts scenario parameters plus an effective seed, and emits one JSON result to stdout; only the production runner writes PostgreSQL.
- User-approved sequencing: exact template import and parity first (P3A), canonical model/container second (P3B), Translator integration afterward (P4).

## Resume instructions

1. Read `FEATURE.md`, `AGENTS.md`, this handoff, and the P4-related slices and component READMEs.
2. Specify P4 before dispatch: canonical-model parity automation in the paper-local Translator, scenario parameterization, generated production runner/result contract, and component-local tests.
3. Dispatch P4 implementation through a dedicated Orca child worktree using Pi with `ai.forge/qwen3.8-27b-nvfp4`; require runtime attestation.
4. Dispatch independent review into the same worktree using Pi with `ai.forge/glm`.
5. Run the smoke tier at settlement with the user-confirmed cluster environment (`k3s-test`, published digest set, `SKIP_BUILD=1`) whenever the integration changes deployed images or manifests; otherwise `make test-fast` gates the slice.
6. Land approved work only on `PADS-paper`; do not merge to `main` without explicit user approval.
