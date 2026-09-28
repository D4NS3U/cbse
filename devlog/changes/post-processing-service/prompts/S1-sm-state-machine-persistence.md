# Dispatch prompt — S1: SM state machine & persistence (Task-spec-contract output)

**Task title:** S1 — SM state machine & persistence: `Finished` state, evaluation publication guards, runner-round bookkeeping and round-scoped Jobs

**Read order (mandatory, all normative and read-only for you):** 1) `devlog/changes/post-processing-service/FEATURE.md` (umbrella — §5 global constraints verbatim, §3 workflow decisions D3/D4/D5/D7 and the rulings record, §6 slice plan); 2) `devlog/changes/post-processing-service/slices/S1-sm-state-machine-persistence.md` (your slice — fully self-contained; it owns every concrete requirement, the Target file list, the schema policy, and the nine evidence blocks); 3) repository-root `AGENTS.md`. On any contradiction: stop and ask via your ask channel.

**Target (five-part contract, summarized — the slice file is normative):** you own exactly six packages under `scenario-manager/internal/`: `persistence`, `lifecycle`, `effectivejob`, `jobadapter`, `runnerstart`, `observation`. You add the `Finished` scenario terminal state (guarded from `PostProcessing`), the additive round/evaluation bookkeeping columns with their guarded persistence primitives (round-claim transition, evaluation publication guards mirroring the translation trio, generalized computed-reps with per-round clamps and the cross-round total), round support in `effectivejob` (round-1 Job names byte-identical to today; `-r<round>` suffix only from round 2; new `experiment.cbse.terministic.de/runner-round` reserved label), round-aware `jobadapter` create/parse/observe, `round_reps`-driven `runnerstart`, current-round `observation` lookup, and the lifecycle doc-comment corrections. Everything else — messaging/`subject`/`nats`/`communication` (S2), the operator (S3), the PPS module (S4), harness/e2e (S5/S6), `api/**`, `Makefile`, `go.mod`/`go.sum` — is out of bounds.

**Change/Constraints (binding summary; the slice file enumerates all twelve numbered requirements):** schema policy verbatim (additive, NULL/DEFAULT-safe, no ALTER repair, `ErrSchemaIncompatible`; fresh per-experiment Core DBs ⇒ no migration path); round-1 observability frozen (single-round Job names byte-identical, `number_of_computed_reps == number_of_reps` at round-1 completion — the S07-A3 harness pin); `PostProcessing` stays in `nonTerminalFailureStates`, `Finished` is never failable; no new module dependencies; no cluster operations; no commits (P6); `make test-fast` rc=0 is mandatory evidence. Attestation protocol below is mandatory.

**Ownership:** sole owner of the six packages in this wave; no parallel wave-mate. Discovered gaps → report lines in your summary, do not fix.

**Observable acceptance:** the nine evidence blocks in the slice file, all echoed in your report — the manager re-runs each independently from a fresh shell before accepting settlement (including re-running `make test-fast` and the targeted package tests).

**Mandatory protocol for this worker (no exceptions):**
- **Runtime attestation, first checkpoint:** run `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"`; repeat verbatim in your `worker_done` executive summary; if it is not `ai.forge/qwen3.8-27b-nvfp4`: stop immediately, report `--outcome failed` with the observed line.
- **No commits (P6):** all changes stay in the working tree; the user commits.
- **Read-only specs:** `FEATURE.md`, slice files, `MANAGER.md`, all `devlog/**`, `AGENTS.md` — you never edit them.
- **Heartbeats and follow-ups:** read coordinator follow-ups at each natural checkpoint and once more immediately before `worker_done` via `orchestration check --terminal <your_handle> --json`.
- **Completion:** report `worker_done` exactly once with: three-sentence executive summary; both lifecycle IDs (task id and dispatch id from your preamble); explicit `--outcome succeeded|failed`; the verbatim attestation line; all nine evidence-block outputs per the slice file; `--files-modified` with your real file list.
