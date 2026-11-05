# PADS paper implementation handoff

## Current state

Feature branch: `PADS-paper`

Current landed implementation: P2 at `1d5ad7756f07a0a5c69f5fe6b2187e1d5b296cff`

Next slice: P3 — template import and `erlang_a_v1` model

`main` remains untouched. Paper-specific implementation ownership is rooted at `pads-paper/`; the shared `component-templates/**` tree remains the reusable baseline and must not be modified for the paper model.

## Slice ledger

| Slice | State | Implementation evidence | Independent review | Landing |
|---|---|---|---|---|
| P1 — model selection and initial-results documentation | complete | `0af44db` plus literature expansion `a1a8c11` | Literature worker metadata/source verification; documentation checks passed | landed on `PADS-paper` |
| P2 — paper component workspace foundation | complete | Task `task_ceac1745e6b4`, dispatch `ctx_e3b56683b6f0`; Pi runtime `ai.forge/qwen3.8-27b-nvfp4`; child HEAD `1d5ad7756f07a0a5c69f5fe6b2187e1d5b296cff`; exact eight README-only files; `git diff --check` and `make test-fast` passed | Task `task_78837d09ec33`, dispatch `ctx_245da2d5ddb3`; verdict `approve`; Pi runtime `ai.forge/glm`; reviewer reran exact-tree, baseline/ruling, hygiene, link, diff, and `make test-fast` checks (77 packages, zero failures) | fast-forwarded to `PADS-paper` at `1d5ad77` |
| P3 — template import and `erlang_a_v1` model | incomplete | not dispatched | not dispatched | not landed |
| P4 — generated result and component integration | incomplete | not dispatched | not dispatched | not landed |
| P5 — end-to-end acceptance | incomplete | not dispatched | not dispatched | not landed |
| P6 — paper-result bundle | incomplete | not dispatched | not dispatched | not landed |

## Settled P2 contracts

- `pads-paper/` is the exclusive ownership root for paper-specific implementations.
- The EDS, Translator, PPS, and Scenario Detail Database must start from their existing `component-templates/**` implementations and receive only Erlang-A-required extensions.
- The simulation model starts from the Translator `runnermod` contract; its container starts from `runner-base` and the generated-runner Dockerfile contract.
- The Result Database preserves the existing official-PostgreSQL/no-repository-init behavior because no standalone repository Result DB template exists.
- Shared Experiment Operator, Scenario Manager, alpha4 CRD, and root test harness remain unchanged unless the user explicitly approves a later contradiction ruling.
- P2 is documentation-only; no executable component or completed-model claim exists yet.

## Resume instructions

1. Read `FEATURE.md`, `AGENTS.md`, this handoff, and all `pads-paper/**/README.md` files.
2. Specify P3 before dispatch, including exact code-import ownership, canonical model-source/synchronization rules, container behavior, and unit/invariant tests.
3. Dispatch implementation through a dedicated Orca child worktree using Pi with `ai.forge/qwen3.8-27b-nvfp4`; require runtime attestation.
4. Dispatch independent review into the same worktree using Pi with `ai.forge/glm`.
5. Land approved work only on `PADS-paper`; do not merge to `main` without explicit user approval.
