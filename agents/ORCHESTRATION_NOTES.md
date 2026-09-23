# Orchestration notes

Durable companion notes for the CBSE manager role defined in root [`MANAGER.md`](../MANAGER.md). Entries record settled orchestration state that live tooling still surfaces (attention flags, `nextAction` suggestions, projection liveness), so future coordinator sessions do not re-investigate or re-mutate them. New entries are appended at the top, dated, with provenance.

---

## 2026-09-23 — September residual terminal ledger rows are inert; ignore them

**Audience:** any future CBSE manager/coordinator session (Orca orchestration layer).
**Decision (user, 2026-09-23):** these residual terminals are *no longer available, no longer necessary, and can be ignored.*

### What these rows are

After the Orca app/runtime was restarted following the September 2026 orchestration sessions, five terminal-ledger rows kept referencing terminals from the old runtime incarnation. Those terminals no longer resolve in the current runtime. Any `worker-release` against them returns the terminal receipt `release_unknown` — `"The recorded terminal no longer resolves; whether its process is gone cannot be proven."` — and exits as the CLI's documented dead-end. `worker-show` observation reports `status: "missing"`, `exactWorker: false`; fleet projection shows `liveness: "unverifiable"`, `attention.requiresAction: true`, and a `nextAction` suggesting `orchestration worker-release`. **All of that is stale echo — none of it authorizes further work.**

### The five rows (as of 2026-09-23)

| Dispatch | Run | Task / work | Worker outcome | Disposition (2026-09-23 session) | Ledger state left standing |
|---|---|---|---|---|---|
| `ctx_ddb33690bd64` | run_08b7d2814e74 | task_3b9545d3ee82 — Slice 07 (fold + cutover + smoke) | succeeded | `worker-release` attempted twice with fresh request IDs; `worker-abandon` refused by system (`dispatch_inactive` — succeeded dispatches cannot be abandoned) | `release_unknown` — inert residue |
| `ctx_9ecfd5c0dd06` | run_08b7d2814e74 | task_0fe6af3261e5 — Slice 06.5 (Alpha4 SM composition) | succeeded | `worker-release` attempted with fresh request ID; abandon not possible for succeeded dispatches | `release_unknown` — inert residue |
| `ctx_60e7168ca8d8` | run_08b7d2814e74 | task_464596413575 — early Slice 07 attempt | failed | `worker-release` attempted (same unresolvable-terminal receipt); then explicitly **fenced via `worker-abandon`** — accepted, `state: failed` | dispatch fenced; terminal row `release_unknown` |
| `ctx_3c91f64fe3a0` | run_08b7d2814e74 | task_bdb99d1e233b — early Slice 05 attempt | failed | same: fenced via `worker-abandon` — accepted, `state: failed` | dispatch fenced; terminal row `release_unknown` |
| `ctx_f3437f93a47e` | run_08b7d2814e74 | task_91a0804fea1a — Slice 06 (Runner Job orchestration) | succeeded | `retained` during the September session; user confirmed 2026-09-23 it is no longer needed — leave as-is, do not attempt release | `retained` — inert residue |

### Why no further action is possible or required

- All five dispatches are **settled** (succeeded/completed or failed) and all five tasks are `completed`; the two `failed` dispatches above were superseded by later successful attempts (their tasks completed via other dispatches). No pending work exists behind any of these rows.
- Release is provably impossible in the current runtime (reproduced with fresh request IDs on 2026-09-23), `worker-abandon` is structurally refused for succeeded dispatches, `worker-stop` has nothing proven to close (`observation: missing`), and `worker-retain` would misstate intent. Retrying any of these only replays the same receipts (see the manager contract's *Safe failure floor*).
- These rows do **not** violate the manager contract's end-of-turn invariant: `worker-list --run <run_id> --terminal-state reclaimable` returns **0 rows** for both September runs as of 2026-09-23.

### Guidance for future coordinator sessions

1. Do not re-run `worker-release`, `worker-stop`, or `worker-abandon` on any dispatch listed above; do not act on their projected `nextAction` or `requiresAction` output.
2. Their historical runs (`run_08b7d2814e74`, `run_603df7d0eb9e`) are fully accounted: run `run_08b7d2814e74` = 15 tasks (13 completed; 2 failed attempts both superseded by completed twins — see `devlog/changes/simrunner-start-and-container-creation/IMPLEMENTATION_HANDOFF.md`); run `run_603df7d0eb9e` = 12 tasks, all completed.
3. If a future Orca runtime migration or database cleanup tool ever makes these terminals resolvable again, a single sweeping `worker-release` per dispatch remains the sanctioned closer — until then, ignore.

*Provenance: verified live on 2026-09-23 by the session manager via `worker-list --include-remote`, `worker-show`/`worker-show --dispatch`, and fresh-ID `worker-release` receipts (requests `37703c73…`, `a9ae6060…`, `b33adbce…`, `a66b47fe…`, plus the fencing `worker-abandon` requests).*
