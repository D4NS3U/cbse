# Slice E3 — The SM's finalization protocol (`scenario-manager/`)

Normative and self-contained for its worker. Read order and global constraints: [../FEATURE.md](../FEATURE.md) §5 (verbatim discipline + the parallelism contract pin — the SBO wire shapes are byte-pinned there), the SBO ruling + its **diagnostic resolution** (the InProgress diagnostic is written by the aggregate pass; the field-ownership amendment is exactly as written there — no wider), §6 E3 row; then repository-root `AGENTS.md`. Your landed-code anchors: `internal/communication/communication.go` (the wire structs + validation — the pre-existing fields are the source of truth), `internal/nats/edsbatch.go` (the batch consumer + the availability handling), `internal/persistence/{eds_intake.go,project.go,schema.go}` (the intake, the project lifecycle incl. `RegisterProject`/`DeleteProjectByNamespaceAndName`, the startup DDL pattern), `internal/aggregate/scheduler.go` (the verdict pass; `verdictForCounts` at line 228). This slice is part of a parallel wave: E1 owns `component-templates/experimental-design-service/**` (+ `go.work` + three Makefile line-contacts) and E2 owns `experiment-operator/**` in the same worktree — their uncommitted files are expected co-residency, never yours to touch. All three documents are read-only for you. On any contradiction between this slice and live code that inspection cannot resolve: stop and ask via your ask channel.

## Target

Exactly these surfaces:

- `scenario-manager/internal/communication/` — the three struct additions (below).
- `scenario-manager/internal/nats/` (`edsbatch.go` and the availability responder implementation — locate it by the `EDSAvailabilityResponder` interface) — the echo, the final-batch handling, the post-final rejection.
- `scenario-manager/internal/persistence/` — the durable `finalized` marker: one additive table in the `schema.go` startup DDL pattern + the mark/read functions.
- `scenario-manager/internal/aggregate/` — the finalization-aware verdict rule + the InProgress diagnostic message patch (one new Kube patch surface on the scheduler's `Kube` interface).
- The SM unit/envtest for the whole matrix.

Everything else is untouchable — in particular `component-templates/**` (E1), `experiment-operator/**` (E2), `test/**` (E4), `devlog/**`.

## Normative wire (byte-pinned by FEATURE.md §5 — identical to E1's goldens; the manager byte-compares the two sides at the wave settlement)

- `EDSAvailabilityRequest` gains `ExperimentType string` with `json:"experiment_type,omitempty"`.
- `EDSAvailabilityReply` gains `ExperimentType string` with `json:"experiment_type,omitempty"` (the echo).
- `ScenarioBatch` gains `Final bool` with `json:"final,omitempty"`.
- Everything else in these structs (and in `ScenarioRecord`/`ScenarioBatchAck`) is byte-identical to the landed code — no tag changes, no reordering, no new fields. The landed EDS-facing payloads are plain (non-strict) decodes; keep them plain (no `UnmarshalJSON` additions).

## Change

1. **The struct additions** — per the wire above, with doc comments in the landed style (the field's wire semantics: absent = legacy/not-final).
2. **The availability echo** — in the availability responder implementation: copy `req.ExperimentType` verbatim into the reply in **both** the `ready` and the `error` replies (status-independent); no declaration (`""`) ⇒ no echo (the legacy reply stays byte-identical — `experiment_type` omitted by omitempty).
3. **The durable finalization marker** — one additive table, added to the `schema.go` startup DDL (the landed `CREATE TABLE IF NOT EXISTS` pattern): `experiment_finalization (project_id INTEGER PRIMARY KEY REFERENCES <the landed project table>(id) ON DELETE CASCADE, finalized_at TIMESTAMP NOT NULL DEFAULT NOW())` — row presence = finalized. The `ON DELETE CASCADE` makes the marker's lifecycle follow the project's (the landed `DeleteProjectByNamespaceAndName` removes the marker with the project). Two functions:
   - `MarkFinalized(ctx, db, namespace, project) error` — idempotent: `RegisterProject` first (a finalization batch may arrive for a not-yet-registered project — note the landed `InsertScenarioBatch` returns early on empty records *before* registering, so the intake must register explicitly in the finalization-only case), then `INSERT ... ON CONFLICT (project_id) DO NOTHING`.
   - `IsFinalized(ctx, db, namespace, project) (bool, error)` — for the aggregate pass (by namespace/project, resolving the project id via the landed `ProjectIDByNamespaceAndName`; a missing project is `(false, nil)`, not an error).
4. **The batch intake** (the `edsbatch.go` consumer path, after the landed identity/reps validation):
   - `batch.Final == true`: insert the scenarios, if any (the landed `InsertScenarioBatch` — an empty batch inserts nothing), then `MarkFinalized`; the ack is the normal `inserted` ack for the inserted count. A repeated final batch is an idempotent no-op (redelivery-safe).
   - `batch.Final == false`/absent **and** the project is already finalized: no insertion; the ack is `status: "error"`, `batch_id` echoed, `received: len(batch.Scenarios)`, `inserted: 0`, `failed: 0`, `reason: "experiment finalized; no further batches accepted"` (the pinned reason string — byte-exact).
   - `batch.Final == false`/absent, not finalized: the landed path, unchanged.
5. **The aggregate pass** (`scheduler.go`):
   - The `Store` interface gains the finalization lookup (or the `Kube`/`Store` split the landed code uses — follow the landed structure).
   - `verdictForCounts` gains the `finalized` input: `c.Failed > 0 → Failed` (unchanged, fail-fast, never waits for `final`); else `finalized && (c.Total == 0 || c.Finished == c.Total) → Finished`; else `""`. The **zero-scenario + explicit Finish ⇒ `Finished` on the empty set** falls out of `c.Total == 0` with `finalized`.
   - The InProgress diagnostic: when the verdict is empty, `finalized == false`, `c.Total > 0`, `c.Finished == c.Total`, **and** the experiment's `status.phase == "InProgress"` (read from the enumerated CR the pass already holds) → merge-patch `status.message = "awaiting EDS finalization"` — write-if-different (skip the patch when the message already equals it), **never** `phase`, **never** `scenarioManagerVerdict`; log an `operation=` line in the landed style (`alpha4_aggregate_awaiting_finalization`). The operator's terminal derivation (later in the sequence — the `Finished` verdict requires the finalization) overwrites the message at the phase transition; the pass skips already-reported experiments (the landed absorbing gate), so it never re-touches the message after the verdict.
   - Update the landed field-ownership test(s) to codify the **additive amendment** exactly as FEATURE.md's diagnostic resolution writes it: the SM never writes `phase`; the SM writes `status.message` in exactly one case (the InProgress diagnostic); the operator retains full ownership of phase and all other messages.
6. **Tests** (unit + envtest, mirroring the landed test style):
   - The verdict matrix: (a) all-terminal without final ⇒ no verdict, (b) all-terminal with final ⇒ `Finished`, (c) zero with final ⇒ `Finished`, (d) zero without final ⇒ no verdict, (e) any `Failed` ⇒ `Failed` regardless of final, (f) partial (some terminal) with final ⇒ no verdict.
   - The echo: declared ⇒ echoed verbatim on `ready` **and** on `error`; undeclared ⇒ no echo (byte-identical legacy reply).
   - The intake: a final batch with scenarios (insert + finalize, normal ack); the finalization-only batch on a not-yet-registered project (project registered, marker set, `inserted: 0`); a non-final batch after finalization (the exact error ack incl. the pinned reason); a repeated final batch (idempotent).
   - The diagnostic: the patch fires only in case (a) with `phase == "InProgress"` (not on other phases, not after a verdict, not when the message already matches); it never touches `phase`/`scenarioManagerVerdict`.
   - The marker lifecycle: `DeleteProjectByNamespaceAndName` removes the marker (cascade).

## Constraints

- The field-ownership amendment is **exactly** the diagnostic resolution in FEATURE.md — no wider (no `phase` write, no second message case, no verdict change).
- The landed verdict behavior is preserved for the legacy state: with no finalization marker, nothing changes except that the `Finished` verdict now requires it — which is the point of the feature; every in-repo batch publisher conforms in the same feature (E1's Finish; the mock + e2e green Finish step land in E4, after yours).
- `Failed` fail-fast and the `Error` flavors are untouched (they never await `final`).
- No cluster operations. `make test-fast` rc=0 is mandatory; **the cluster smoke is the settlement gate**, run by the manager — you do not run `make test-smoke`.
- **Parallel wave-mates:** E1 owns `component-templates/experimental-design-service/**` + `go.work`/`go.work.sum` + three Makefile line-contacts; E2 owns `experiment-operator/**`. Their uncommitted files will appear in `git status` — expected co-residency, never touch them. **Stage only your own partition** (explicit `git add <paths>` — never bare `git add .`), because your commits interleave with the wave-mates' on the shared feature branch.
- **Commits:** your work lands on the feature branch (the worktree's branch, `D4NS3U/experimental-design-service`) — never `main`, never another worker's branch. Every commit carries the traceability trailer `[orchestration: task <task-id> dispatch <dispatch-id>]` (the IDs from your injected preamble). Commit at natural milestones; leave the branch in a test-green state at `worker_done`.
- Global constraints of FEATURE.md §5 apply verbatim (attestation, read-only specs, scope discipline, verification honesty, licensing headers).

## Ownership

Sole owner of `scenario-manager/internal/{communication,nats,persistence,aggregate}/**` (the named additions) + the SM tests. Discovered gaps → report lines in `worker_done`, do not fix.

## Observable acceptance

Run and echo all of these in `worker_done` (the manager re-runs each independently):

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat; mismatch → stop, `--outcome failed`).
2. Containment: `git status --short` — your `scenario-manager/internal/{communication,nats,persistence,aggregate}` files only, plus expected co-resident wave-mate files; zero files outside your partition.
3. `cd scenario-manager && go build ./... && go vet ./...` → rc=0.
4. `make test-fast` rc=0 final receipt (mandatory).
5. Targeted: `cd scenario-manager && go test -race -count=1 ./internal/communication/... ./internal/nats/... ./internal/persistence/... ./internal/aggregate/...` → rc=0.
6. **Wire-proof receipts (the manager's byte-comparison target against E1's goldens):** echo the test names proving the exact marshaled bytes of the four payloads — availability request with declared `standard` (and without, the legacy shape); the reply with the echo on `ready` **and** `error`; the scenario batch non-final (no `final` field in the JSON); the finalization batch (`"scenarios":[]` + `"final":true`); the ack unchanged.
7. Verdict-matrix receipts: echo the six matrix test names (a–f).
8. Intake receipts: echo the test names for the final-with-scenarios, the finalization-only on an unregistered project, the post-final rejection (echo the exact asserted `reason` string), and the repeated-final idempotency.
9. Diagnostic receipts: echo the test names proving the patch fires only in the InProgress all-terminal not-finalized case, is write-if-different, and never touches `phase`/`scenarioManagerVerdict`; plus the field-ownership test name codifying the additive amendment.
10. Commit receipts: the branch HEAD, and `git log --format=%b -3 | grep 'orchestration:'` showing the trailer on your commits.

Completion protocol: `worker_done` exactly once, with a three-sentence executive summary, both lifecycle IDs (task + dispatch from your preamble), explicit `--outcome succeeded|failed`, the verbatim attestation line, the ten evidence blocks, and `--files-modified`. Read coordinator follow-ups at each natural checkpoint via `orca orchestration check --terminal <your_handle> --json`, and once more immediately before `worker_done`.

**Session hygiene (binding, learned from the PPS S1 crash):** keep tool outputs small (`head`/`tail`/`grep -n`, never whole-file cats of large files); gather evidence incrementally as you complete each requirement rather than batching everything into one giant end-session run.
