# Slice S4R — PPS wave-cap parsing: `-max-runners-per-round` 0 = disabled (ruling Q7)

Normative and self-contained for its worker. Read order and global constraints: [../FEATURE.md](../FEATURE.md) §5 (verbatim discipline), §3 (D7 + **ruling Q7** — the complete flag semantics), §6 S4R row; then repository-root `AGENTS.md`. S4 is landed: the module, its clamp logic, and its tests exist — this is a **small parsing-semantics delta on the landed module**, not a re-implementation. This slice extends, never overrides, the umbrella. All three documents are read-only for you. On any contradiction between this slice and live code that inspection cannot resolve: stop and ask via your ask channel.

## Target

Exactly these files under `component-templates/post-processing-service/`:

- `internal/config/config.go` (+ `config_test.go`) — the `-max-runners-per-round` flag semantics.
- `internal/evaluation/evaluation.go` (+ `evaluation_test.go`) — the clamp branch.
- `README.md` — the flag documentation.

Everything else is untouchable — in particular all other module files, `scenario-manager/**`, `experiment-operator/**`, `test/**`, root `Makefile`, `go.work`, `go.mod`/`go.sum` (no dependency changes).

## Change

1. **Config parsing (`internal/config`)** — `-max-runners-per-round` semantics per ruling Q7, replacing the current reject-all-non-positive rule for this one flag:
   - **positive** → per-wave cap at that value (unchanged behavior);
   - **`0` → disabled**: parsed, valid, and logged at startup with an explicit observable line (e.g. `pps: per-wave cap disabled; waves sized by the estimate alone, bounded only by max-replications headroom`) — the disabled state must be visible, not silent;
   - **negative (or non-integer)** → fail-fast configuration error at startup, exactly as today;
   - **unset** → default stays **1000** (ruling Q4's bounded defaults — do not change the default).
   - The `-max-replications` flag is **deliberately untouched**: it is the scientific stopping criterion and stays strictly positive-mandatory (no 0-mode — ruling Q7's operational/scientific separation). Same for `-deterministic-additional-runners` and `-evaluation-policy`.
2. **Evaluation clamp (`internal/evaluation`)** — when the cap is disabled, the per-wave clamp is skipped: the requested wave is `max(min-batch, n_req − n)` bounded **only** by the `MAX_REPLICATIONS` headroom (`additional ≤ max_replications − n`). The min-batch floor and the max-replications headroom remain **always active in all modes** — they are criterion-level, not pacing. The enabled path (positive cap) is byte-for-byte unchanged.
3. **Tests** — config: `0` accepted → disabled state (+ the startup log line asserted), negative → error, positive → cap, unset → default 1000; evaluation: the disabled path passes the raw `n_req − n` through (bounded by the headroom only; min-batch floor intact — include a vector where the raw estimate exceeds the old default cap to prove it flows verbatim), and the enabled path's existing tests stay green as regression.
4. **README** — the flag table row gains the full semantics: positive = cap; 0 = disabled (estimate verbatim within the max-replications headroom, startup-logged); negative = configuration error; default 1000. One sentence on the operational/scientific separation (why `-max-replications` has no 0-mode).

## Constraints

- Surgical delta only: no refactors, no renaming, no behavior changes beyond the specified semantics; the smoke's own args (`30`) and the S5-settled profile are unaffected.
- `make test-fast` rc=0 is mandatory (your module's vet + race lines run inside it).
- No cluster operations, no image builds/pushes, no `make test-smoke` — the rebuilt image rides with S6's settlement smoke.
- Global constraints of FEATURE.md §5 apply verbatim (attestation, no commits, read-only specs, scope discipline, verification honesty, licensing headers).

## Ownership

Sole owner of the six named files (three + their tests... exactly: `config.go`, `config_test.go`, `evaluation.go`, `evaluation_test.go`, `README.md` — five files). Discovered gaps → report lines, do not fix.

## Observable acceptance

Run and echo all of these in `worker_done` (the manager re-runs each independently):

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat; mismatch → stop, `--outcome failed`).
2. Containment: `git status --short` shows only your files inside `component-templates/post-processing-service/`.
3. `cd component-templates/post-processing-service && go build ./... && go vet ./...` → rc=0.
4. `make test-fast` rc=0 final receipt (mandatory).
5. Targeted: `cd component-templates/post-processing-service && go test -race -count=1 ./internal/config/... ./internal/evaluation/...` → rc=0 with the new test names echoed (the 0-mode test, the negative-error test, the disabled-path evaluation vector).
6. grep receipts: the startup log line for the disabled state; the default `1000` unchanged; `-max-replications` validation untouched (still positive-mandatory).

Completion protocol: `worker_done` with a three-sentence executive summary, both lifecycle IDs (task + dispatch from your preamble), explicit `--outcome succeeded|failed`, the verbatim attestation line, the six evidence blocks, `--files-modified`.

**Session hygiene (binding):** keep tool outputs small; gather evidence incrementally; never batch everything into one giant end-session run.
