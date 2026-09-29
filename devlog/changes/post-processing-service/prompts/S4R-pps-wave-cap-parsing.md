# Dispatch prompt — S4R: PPS wave-cap parsing (ruling Q7)

**Task title:** S4R — `-max-runners-per-round` 0 = disabled (estimate verbatim within max-replications headroom, startup-logged), negative = fail-fast, positive = cap, default 1000 unchanged

**Read order (mandatory, all normative and read-only for you):** 1) `devlog/changes/post-processing-service/slices/S4R-pps-wave-cap-parsing.md` (your slice — fully self-contained: the five-file Target, the exact semantics, six evidence blocks; it links FEATURE.md ruling Q7 and D7); 2) `devlog/changes/post-processing-service/FEATURE.md` §5 + the Q7 ruling record entry; 3) repository-root `AGENTS.md`. On any contradiction: stop and ask via your ask channel.

**Target (five-part contract, summarized — the slice file is normative):** you own exactly five files in the landed `component-templates/post-processing-service/` module: `internal/config/{config.go,config_test.go}`, `internal/evaluation/{evaluation.go,evaluation_test.go}`, `README.md`. The delta: `-max-runners-per-round` gains 0 = disabled semantics (startup log line, estimate flows verbatim bounded only by the max-replications headroom — min-batch floor and headroom stay always-active), negative/non-integer = fail-fast (unchanged), positive = cap (unchanged), unset default stays 1000 (unchanged). `-max-replications` is deliberately untouched (scientific stopping criterion, strictly positive-mandatory). Surgical only — no refactors.

**Change/Constraints (binding):** the disabled-path evaluation vector must include a case where the raw estimate exceeds the old default cap (proving verbatim flow); all existing enabled-path tests stay green as regression. `make test-fast` rc=0 mandatory; no cluster ops, no image builds, no `make test-smoke` (the rebuilt image rides with S6's settlement smoke). No commits (P6).

**Mandatory protocol (no exceptions):**
- **Runtime attestation, first checkpoint:** `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` must print `ai.forge/qwen3.8-27b-nvfp4`; on mismatch stop immediately and report `--outcome failed`.
- **Session hygiene (binding):** small outputs, incremental evidence — never a giant end-session batch.
- **Read-only specs:** FEATURE.md, slices, MANAGER.md, devlog/**, AGENTS.md — never edit them.
- **Completion:** `worker_done` exactly once: three-sentence executive summary; both lifecycle IDs (task + dispatch from your preamble); `--outcome succeeded|failed`; verbatim attestation line; the six evidence blocks; `--files-modified`. Read coordinator follow-ups at each natural checkpoint via `orchestration check --terminal <your_handle> --json`, and once more immediately before `worker_done`.
