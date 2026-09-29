# Dispatch prompt — S6: E2E loop spec

**Task title:** S6 — The natural top-up loop, asserted: one new Ordered spec pinning met-path single-wave bookkeeping and loop-path round bookkeeping (runner_round, -r2 Jobs, computed counts, wave-cap observability)

**Read order (mandatory, all normative and read-only for you):** 1) `devlog/changes/post-processing-service/slices/S6-e2e-loop-spec.md` (your slice — fully self-contained: the one-file Target, the spec's exact structure and invariant list, six evidence blocks; it links FEATURE.md rulings Q4–Q7); 2) `devlog/changes/post-processing-service/FEATURE.md` §5 + §6 S6 row; 3) repository-root `AGENTS.md`. On any contradiction: stop and ask via your ask channel.

**Target (five-part contract, summarized — the slice file is normative):** you own exactly `test/e2e/smoke_test.go`, adding ONE new Ordered spec between the chain spec and the idempotence spec — everything else in the file stays byte-identical. The system behavior is already settled and observed (the retained runs of 2026-09-29: met-path `runner_round=1, computed==40`; loop-path `1 → +2 → 3 → +11/+26 [cap 30] → met at 14/29, runner_round=3`); you encode that truth as assertions: an 8-min convergence gate (all four `Finished`), per-family DB bookkeeping (met-path: runner_round==1, computed==number_of_reps==40; loop-path: runner_round>=2 — never pinned equal, computed>1, round_reps<=30 cap observability, evaluation_attempts>=runner_round redelivery tolerance), round-Job identity (exactly two `-r2` Jobs, one per loop scenario; no met-path Job carries a round label >1), and the four-row diagnostics artifact dump. Structural invariants, not observed coincidences. The spec runs in both smoke modes.

**Change/Constraints (binding):** surgical — one new `It`; helpers unchanged; the other five specs byte-identical. `make test-fast` rc=0 mandatory (e2e compile runs inside it). No cluster operations, no `make test-smoke` — the settlement smoke (also re-verifying the S4R image) is the manager's gate. No commits (P6).

**Mandatory protocol (no exceptions):**
- **Runtime attestation, first checkpoint:** `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` must print `ai.forge/qwen3.8-27b-nvfp4`; on mismatch stop immediately and report `--outcome failed`.
- **Session hygiene (binding):** small outputs, incremental evidence — never a giant end-session batch.
- **Read-only specs:** FEATURE.md, slices, MANAGER.md, devlog/**, AGENTS.md — never edit them.
- **Completion:** `worker_done` exactly once: three-sentence executive summary; both lifecycle IDs (task + dispatch from your preamble); `--outcome succeeded|failed`; verbatim attestation line; the six evidence blocks; `--files-modified`. Read coordinator follow-ups at each natural checkpoint via `orchestration check --terminal <your_handle> --json`, and once more immediately before `worker_done`.
