# PADS paper implementation handoff

## Current state

Feature branch: `PADS-paper`

Current landed implementation: P2 at `1d5ad7756f07a0a5c69f5fe6b2187e1d5b296cff`

Next slice: P3A — exact template import

`main` remains untouched. Paper-specific implementation ownership is rooted at `pads-paper/`; the shared `component-templates/**` tree remains the reusable baseline and must not be modified for the paper model.

## Slice ledger

| Slice | State | Implementation evidence | Independent review | Landing |
|---|---|---|---|---|
| P1 — model selection and initial-results documentation | complete | `0af44db` plus literature expansion `a1a8c11` | Literature worker metadata/source verification; documentation checks passed | landed on `PADS-paper` |
| P2 — paper component workspace foundation | complete | Task `task_ceac1745e6b4`, dispatch `ctx_e3b56683b6f0`; Pi runtime `ai.forge/qwen3.8-27b-nvfp4`; child HEAD `1d5ad7756f07a0a5c69f5fe6b2187e1d5b296cff`; exact eight README-only files; `git diff --check` and `make test-fast` passed | Task `task_78837d09ec33`, dispatch `ctx_245da2d5ddb3`; verdict `approve`; Pi runtime `ai.forge/glm`; reviewer reran exact-tree, baseline/ruling, hygiene, link, diff, and `make test-fast` checks (77 packages, zero failures) | fast-forwarded to `PADS-paper` at `1d5ad77` |
| P3A — exact template import | incomplete | specified; not dispatched | not dispatched | not landed |
| P3B — canonical `erlang_a_v1` model and CLI container | incomplete | user rulings settled; detailed slice pending P3A | not dispatched | not landed |
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

1. Read `FEATURE.md`, `AGENTS.md`, this handoff, and all `pads-paper/**/README.md` files.
2. Dispatch the specified P3A exact-import slice and require baseline file-list/byte/mode parity.
3. After P3A settles, specify P3B's CLI input/output, original event algorithm, container, and unit/invariant tests before dispatch.
4. Dispatch implementation through a dedicated Orca child worktree using Pi with `ai.forge/qwen3.8-27b-nvfp4`; require runtime attestation.
5. Dispatch independent review into the same worktree using Pi with `ai.forge/glm`.
6. Land approved work only on `PADS-paper`; do not merge to `main` without explicit user approval.
