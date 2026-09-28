# Slice S1 — SM state machine & persistence: `Finished`, evaluation guards, runner rounds

Normative and self-contained for its worker. Read order and global constraints: [../FEATURE.md](../FEATURE.md) §5 (verbatim discipline), §3 (esp. D3, D4, D5, D7 and the rulings record — Q1–Q5 all answered), §6 S1 row; then repository-root `AGENTS.md`. This slice extends, never overrides, the umbrella. All three documents are read-only for you. On any contradiction between this slice and live code that inspection cannot resolve: stop and ask via your ask channel.

## Target

Exactly these packages (source + `_test.go` companions), all under `scenario-manager/internal/`:

- `persistence/` — `db.go` (state constants), `schema.go` (DDL + validation), `scenario_status.go` (guarded transitions), `runner_status.go` (computed-reps + failure lists), `eds_intake.go` (intake insert)
- `lifecycle/` — `lifecycle.go` (labels, `RunnerJobName`, state-list consumers), `terminal.go`, `jobownership.go` (Job-identity consumers; doc comments included)
- `effectivejob/` — `effectivejob.go`
- `jobadapter/` — `jobadapter.go`, `cradapter.go`, `indexes.go`
- `runnerstart/` — `scheduler.go`, `store.go`, `worker.go`
- `observation/` — `scheduler.go`, `store.go`, `worker.go`

Everything else is untouchable, in particular: `internal/{subject,nats,communication,ready,config,translatorconfig,informer,core}` (S2's or untouched), `experiment-operator/**`, `component-templates/**`, `api/**`, `test/**`, the root `Makefile`, `docs/**`, `devlog/**`, `go.mod`/`go.sum` (no new dependencies — `gonum` belongs to S4, not you).

## Change

1. **`Finished` state.** Add `ScenarioStateFinished = "Finished"` to the constants in `persistence/db.go`. Add guarded `MarkScenarioFinished(ctx, db, scenarioID) (bool, error)` in `scenario_status.go`: single-row `UPDATE ... SET state = Finished, updated_at = NOW() WHERE id = $1 AND state = 'PostProcessing'`, same shape and error discipline as `MarkScenarioPostProcessing`. `Finished` is terminal: it must **not** be added to `nonTerminalFailureStates` (`runner_status.go` — `MarkScenarioFailedFrom` then rejects `from = Finished` automatically via `failureAllowList`), and must **not** appear in `terminalFailureStatesString` (`lifecycle/lifecycle.go` ~line 245–250 — that list enumerates failable/non-terminal states and keeps `PostProcessing` as a member, unchanged).
2. **Round bookkeeping columns** (additive, in `schema.go` DDL and its validation; `eds_intake.go` supplies intake values):
   - `runner_round INT NOT NULL DEFAULT 1` — the current runner round.
   - `round_reps INT NOT NULL` — the current round's requested repetition count; intake inserts `round_reps = number_of_reps` (round 1), so the single code path always reads `round_reps`.
   - `round_computed_reps INT NOT NULL DEFAULT 0` — completed reps of the current round (monotone within the round, clamped to `round_reps`; reset to 0 by item 4's round-claim transition).
   - Evaluation-publication guards mirroring the translation trio: `evaluation_attempts INT NOT NULL DEFAULT 0`, `evaluation_publish_started_at TIMESTAMPTZ NULL`, `evaluation_request_published_at TIMESTAMPTZ NULL`.
   - Schema policy verbatim (FEATURE.md §5): additive and NULL/DEFAULT-safe; no ALTER repair; a table missing/mis-typing the new columns returns `ErrSchemaIncompatible` (startup fails). Per-experiment Core DBs are created fresh by the operator, so migration of old tables is a non-goal — state that in a comment where the columns are defined. The intake `INSERT` in `eds_intake.go` gains the round-1 values; keep the one-transaction batch insert.
3. **Evaluation publication guard functions** (S2 wires them into messaging; you own the persistence primitives, mirroring `ClaimScenarioForTranslation` / `MarkTranslationPublishStarted` / `MarkScenarioTranslationRequestPublished` in `scenario_status.go`):
   - `ClaimScenarioForEvaluation(ctx, db, scenarioID) (attempt int, ok bool, err error)` — claims exactly rows in `PostProcessing`, increments `evaluation_attempts`, returns it; missing/no-longer-`PostProcessing` → `(0, false, nil)`.
   - `MarkEvaluationPublishStarted(ctx, db, scenarioID, attempt) (bool, error)` — sets `evaluation_publish_started_at = NOW()` only for the exact `PostProcessing` claim with that attempt number (stale attempt → `false, nil`).
   - `MarkEvaluationRequestPublished(ctx, db, scenarioID, attempt) (bool, error)` — same exact-attempt guard for `evaluation_request_published_at`.
4. **Round-claim transition.** `ClaimScenarioForEvaluationRound(ctx, db, scenarioID, additionalRunners) (round int, ok bool, err error)` in `scenario_status.go`: guarded single-row `UPDATE ... SET state = 'StartingRunners', runner_round = runner_round + 1, round_reps = $additional, round_computed_reps = 0, updated_at = NOW() WHERE id = $1 AND state = 'PostProcessing'` with `additionalRunners >= 1` validated in Go (reject with a typed error otherwise); returns the new `runner_round`. This is the `PostProcessing -> StartingRunners` edge of FEATURE.md D4; the S2 verdict handler will call it.
5. **Computed-reps generalization** (`runner_status.go`): `number_of_computed_reps` becomes the **total across all rounds** (drop the `LEAST(number_of_reps, ...)` clamp; monotone `GREATEST` semantics stay), and `round_computed_reps` tracks the current round (clamped to `round_reps`). Redesign the update function (e.g. `UpdateScenarioComputedRepsForRound(ctx, db, scenarioID, round, roundCount)`) so that: per-round monotone + clamp; the total accumulates as `GREATEST(total, total - prior_round + new_round)`; and the **single-round equality pin** holds: after round 1 completes, `number_of_computed_reps == number_of_reps` (the S07-A3 harness assert must stay true — prove it with a test).
6. **effectivejob round support** (`effectivejob.go`): new validated inputs — `round` (≥ 1) and the per-round completion count (completions = `round_reps`; for round 1 the caller passes what today arrives as `number_of_reps`, so round-1 behavior is byte-identical). Job name: **round 1 keeps today's exact format `simrun-<uid12>-s<scenario-id>-a<attempt>`** (built in `lifecycle.RunnerJobName`); round ≥ 2 appends `-r<round>`. Add reserved label `experiment.cbse.terministic.de/runner-round` (always set, round 1 = `"1"`) following the `LabelTranslationAttempt` convention in `lifecycle.go` (~line 51–54) and `reservedLabels` in `effectivejob.go` (~line 260–270); extend `mergeLabels` usage accordingly.
7. **jobadapter round awareness** (`jobadapter.go`, `cradapter.go`, `indexes.go`): parse both name forms (no `-r` suffix ⇒ round 1; `-r<round>` ⇒ that round); create/confirm/observe semantics scoped to (scenario, attempt, round). The round-2 create must succeed after the round-1 Job completed — different names must never collide; prove with a test.
8. **runnerstart** (`scheduler.go`, `store.go`, `worker.go`): the StartingRunners workflow reads `round_reps` as the Job's completion count and passes the scenario's `runner_round` into effectivejob. Round-1 flow byte-identical (`round_reps == number_of_reps` from intake). The bounded-scheduler claim semantics otherwise unchanged.
9. **observation** (`scheduler.go`, `store.go`, `worker.go`): the Job lookup for a scenario uses the **current round's** name (the scenario row carries `runner_round`); completion of the round's Job applies the existing guarded `InProcessing -> PostProcessing` transition (round-agnostic, unchanged); computed-reps updates go through the item-5 generalized function with per-round counts.
10. **lifecycle consumers** (`lifecycle.go`, `terminal.go`, `jobownership.go`): no membership changes to the state lists (item 1); update any doc comment that claims `PostProcessing` is (the) terminal state — it is now the evaluation-boundary state from which both `Finished` and a new round begin. Extend Job-identity parsing/naming helpers for the round suffix per their semantics (e.g. `RunnerJobName` gains the round parameter; `jobownership` label-based pre-filtering is unaffected since the round rides the new label).
11. **Eventlog**: the two new transitions (`PostProcessing -> Finished`, `PostProcessing -> StartingRunners` round-claim) record eventlog entries following the existing conventions in the transition call sites (read how `MarkScenarioPostProcessing`'s callers log; mirror).
12. **Tests** (all in the slice's packages): guard rejections (wrong from-state, `additionalRunners < 1`, stale exact-attempt no-ops, re-claim idempotence), schema validation accept/reject for the new columns, round-1 Job-name **byte-equality regression** (exact `simrun-<uid12>-s<id>-a<attempt>` string), round-2 name/label tests, jobadapter round parse + post-completion round-2 create, computed-reps invariants (monotone, per-round clamp, cross-round accumulation, single-round equality pin), eventlog records. The race suite must stay green.

## Constraints

- **Schema policy** (FEATURE.md §5): additive/NULL-safe columns, no ALTER repair, `ErrSchemaIncompatible` on mismatch; fresh per-experiment Core DBs ⇒ no migration path is built.
- **Round-1 observability is frozen**: single-round flows produce byte-identical Job names and the same observable bookkeeping as today (`number_of_computed_reps == number_of_reps` at round-1 completion; harness/e2e pins must not break).
- **`PostProcessing` stays in `nonTerminalFailureStates`** — the terminal sweep and the D7 stop-unmet verdict both fail scenarios from it; `Finished` is never failable.
- No messaging/subject/communication changes (S2 owns them); no `go.mod`/`go.sum` edits; no cluster operations; no harness changes.
- `make test-fast` rc=0 is mandatory evidence (this is a Go-only slice: unit + race lines).
- Global constraints of FEATURE.md §5 apply verbatim (attestation, no commits, read-only specs, scope discipline, verification honesty).

## Ownership

Sole owner of the six listed packages in this wave. No parallel wave-mate is active. Everything outside the Target list is out of bounds; discovered gaps → report lines, do not fix.

## Observable acceptance

Run and echo all of these in `worker_done` (the manager re-runs each independently from a fresh shell):

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat in your executive summary; mismatch → stop, change nothing, `--outcome failed`).
2. Containment: `git status --short` shows only files inside your six packages.
3. `cd scenario-manager && go build ./... && go vet ./...` → rc=0 receipts.
4. `make test-fast` rc=0 final receipt (mandatory — echo the tail line with the rc).
5. Targeted receipts: `cd scenario-manager && go test ./internal/persistence/... ./internal/effectivejob/... ./internal/jobadapter/... ./internal/runnerstart/... ./internal/observation/... ./internal/lifecycle/...` → rc=0.
6. Round-1 regression proof: echo the test name + assertion line proving the round-1 Job name equals today's exact format.
7. Schema proof: echo the test name proving validation rejects a table missing the new columns.
8. grep receipts: `ScenarioStateFinished` constant; the six new columns in the DDL; `nonTerminalFailureStates` membership unchanged (5 members, no `Finished`).
9. Single-round equality pin: echo the test name proving `number_of_computed_reps == number_of_reps` after round 1.

Completion protocol: `worker_done` with a three-sentence executive summary, both lifecycle IDs (task + dispatch from your preamble), explicit `--outcome succeeded|failed`, the verbatim attestation line, and the nine evidence blocks.
