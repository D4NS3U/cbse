# Slice E1 — Reference ExperimentalDesignService module (`component-templates/experimental-design-service/`)

Normative and self-contained for its worker. Read order and global constraints: [../FEATURE.md](../FEATURE.md) §5 (verbatim discipline + the parallelism contract pin + the canonical Detail DB schema), §3 (esp. D1, D2, D3, D4, D7, D8, D9 and the rulings record R1–R6 + the SBO ruling), §6 E1 row; then repository-root `AGENTS.md`. Your module archetype is `component-templates/post-processing-service/` (go.mod, `cmd/`, `internal/{config,wire,messaging}` layout, Dockerfile, README, test discipline) and `component-templates/translator/` (the five-field `dbconfig` Secret contract, the `detaildb` read pattern, the Dockerfile layering). This slice is part of a parallel wave: E2 owns `experiment-operator/**` and E3 owns `scenario-manager/**` in the same worktree — their uncommitted files are expected co-residency, never yours to touch. All three documents are read-only for you. On any contradiction between this slice and live code that inspection cannot resolve: stop and ask via your ask channel.

## Target

Exactly these surfaces — all new files unless named otherwise:

- `component-templates/experimental-design-service/` — a new Go module `github.com/D4NS3U/cbse/component-templates/experimental-design-service` with: `go.mod`/`go.sum`; `cmd/experimental-design-service/main.go`; `internal/config` (env + Secret parsing/validation, fail-fast at startup); `internal/wire` (the four protocol payloads per the normative contract below, golden-tested); `internal/recipestore` (the pgx design-layer writer: the six-object self-provision DDL, the entry CRUD, the recipe locator rows, the single-transaction save, the id rules); `internal/server` (the `net/http` JSON API + the embedded static UI page); `internal/publisher` (the NATS connect → availability handshake → JetStream batch publish, the mock's retry skeleton); `Dockerfile` (static scratch binary mirroring the PPS/translator discipline, `USER 1000:1000`); `README.md` (component doc: purpose, the wire + API contracts, config, the four-step flow); Apache-2.0 headers everywhere (`Copyright 2025-2026 Daniel Seufferth`).
- `go.work` — add the module to the `use` list; keep the replace block intact; add the needed `go.work.sum` entries (`go work sync`); no version churn in the other modules (use the same nats.go/pgx versions the sibling modules pin).
- **Manager-licensed Makefile addition (exactly three line-contacts):** the root `Makefile` `test-fast` recipe enumerates modules; add `component-templates/experimental-design-service` to (a) the `gofmt` `find` path list, (b) a `cd component-templates/experimental-design-service && go vet ./...` line beside the sibling modules', (c) a `cd component-templates/experimental-design-service && go test -race -timeout 30m ./...` line beside the siblings'. Nothing else in the Makefile is yours (`test/harness/**` stays untouched — the harness self-test inside test-fast must keep passing unmodified).

Everything else is untouchable — in particular `scenario-manager/**` (E3's parallel wave), `experiment-operator/**` (E2's), `test/**` (E4's), `api/**`, `docs/**`, `devlog/**`.

## Normative contracts

### Env (injected by the operator per E2/D2 — validated fail-fast at startup, translator style)

- `NATS_URL` — non-empty.
- `EDS_AVAILABILITY_SUBJECT` — must be exactly `cbse.<SIMULATIONPROJECTNAMESPACE>.<SIMULATIONPROJECTNAME>.eds.scenarios.available`.
- `SIMULATIONPROJECTNAMESPACE`, `SIMULATIONPROJECTNAME`, `SIMULATIONEXPERIMENTUID` — the downward-API identity; non-empty. Every payload's `project` field must equal `SIMULATIONPROJECTNAME` (a mismatch is poison: refuse with a descriptive error).
- Detail DB connection — the mounted Secret at `/detaildb-connection/` (keys `host`, `port`, `dbname`, `user`, `password`; the same five-field contract the translator's `dbconfig` validates: all present, port in 1..65535; `sslmode=disable`).
- Nothing else. The batch subject comes from the SM's availability reply at runtime (never baked, never from env).

### Wire (byte-pinned by FEATURE.md §5 — golden-tested; the landed SM structs in `scenario-manager/internal/communication/communication.go` are the source of truth for the pre-existing fields, the ruled additions are named there)

- **Availability request** (you publish): `{"batch_id":"<id>","project":"<exp name>","scenario_count":N,"experiment_type":"standard"}` — the reference EDS always declares `experiment_type: "standard"`.
- **Availability reply** (you consume): `{"status":"ready"|"error","reason":"…","batch_subject":"…","experiment_type":"…"}` — require `status=="ready"` and a non-empty `batch_subject`; **require the verbatim echo** `experiment_type == "standard"` (a missing or mismatched echo is a hard protocol error — clean, UI-visible, retryable on the next Start; it indicates an SM without the finalization protocol).
- **Scenario batch** (you publish): `{"batch_id":"<id>","project":"<exp name>","scenarios":[{"priority":N,"number_of_reps":N,"recipe_info":{"parameterset_id":N},"confidence_metric":<finite > 0>}]}` — `final` absent. `number_of_reps` in `1..100000` (the SM's landed guard `MinReps..MaxReps`), `priority` > 0.
- **Finalization batch** (you publish on Finish): `{"batch_id":"<id>","project":"<exp name>","scenarios":[],"final":true}` — the empty array is present, `final` is `true`.
- **Batch ack** (you consume): `{"status":"inserted"|"poison"|"error","batch_id":"<id>","received":N,"inserted":N,"failed":N,"reason":"…"}` — surface `status` + `reason` in the API response; `inserted` is success.

Your `wire` types are your own Go structs (Go internal-package rules forbid importing `scenario-manager/internal/**`) pinned to the contract above by golden-JSON tests — field-for-field, including the omitempty behavior (a non-final batch marshals without `final`).

### Detail DB (the canonical schema in FEATURE.md — verbatim DDL, ruled R2/R3)

- **Self-provision at startup:** execute exactly the canonical six-object DDL from FEATURE.md (`CREATE TABLE IF NOT EXISTS` × 5, `CREATE VIEW IF NOT EXISTS` × 1) before serving; a no-op against a pre-created matching schema (both delivery branches — the seeded reference image and a user-pre-created host schema — must work).
- **Id assignment:** per table, next id = `MAX(id)+1` with the floor 5 (an empty table or one holding only the baseline 1–4 assigns 5). The EDS **never writes ids 1–4** in any of the five tables — an attempted write is a clean refusal (the API returns a descriptive error; the UI surfaces it).
- **Save semantics (one transaction):** upsert the design's entries (INSERT newly assigned ids; UPDATE the values of ids this process assigned before — the live-locators ruling: a saved design's resolved values track its entries until the next save) and the recipe locator rows (INSERT new `parameterset_id`s; UPDATE the four FK columns of ids this process assigned). The in-memory design state tracks which ids the process owns; a process restart starts with an empty design (the ruled in-memory state) — the saved rows remain in the DB, and a fresh design gets fresh ids from 5 upward.
- **Value domains** (mirror the DDL `CHECK`s, refuse out-of-domain values at the API): `arrival_rate`/`service_rate`/`run_duration` positive integers; `seed_policy` integer ≥ 0.
- **Reads:** `GET /api/state` includes `db_rows` — the full `simulation_parameters` view projection (`parameterset_id, arrival_rate, service_rate, run_duration, seed_policy`, all rows, baseline included). The pre-flight (below) SELECTs the view per referenced `parameterset_id`.

### API surface (the embedded UI is a thin Facade over this; the e2e drives the same API — ruling R1)

- `GET /` — the embedded UI: one static HTML+JS page served via `go:embed`. It renders the state panel and the four action areas (Design / Save Experiment / Start Experiment / Finish Experiment); every button maps to exactly one endpoint below; after a successful Finish the Start and Finish controls are disabled client-side (finalization is irrevocable — the server also enforces it).
- `GET /api/state` — `{design:{entries:[{id,parameter,value}],recipes:[{parameterset_id,arrival_rate_id,service_rate_id,run_duration_id,seed_policy_id}],scenarios:[{index,priority,number_of_reps,confidence_metric,parameterset_id,published:bool}]},flow:{saved:bool,started:bool,finalized:bool,last_handshake:{status,reason?,batch_subject?},last_publish:{status,reason?,ack?}},db_rows:[{parameterset_id,arrival_rate,service_rate,run_duration,seed_policy}]}`.
- `POST /api/entries` — body `{parameter:"arrival_rate"|"service_rate"|"run_duration"|"seed_policy",value:int}` → `{id}` (in-memory assignment per the id rules; the value-domain check applies).
- `POST /api/recipes` — body `{arrival_rate_id,service_rate_id,run_duration_id,seed_policy_id}` (all must reference entries assigned in this process) → `{parameterset_id}`.
- `POST /api/scenarios` — body `{priority:int>0,number_of_reps:int 1..100000,confidence_metric:finite > 0,parameterset_id:int}` (the parameterset must reference a recipe in this process) → appends; the scenario is unpublished.
- `DELETE /api/scenarios/<index>` — removes an **unpublished** scenario; a published one is refused (it already exists in the SM).
- `POST /api/save` — the single-transaction save (entries + recipes per the save semantics) → `{saved_entries:N,saved_recipes:M}`.
- `POST /api/start` — the pre-flight: every scenario's `parameterset_id` must resolve in the `simulation_parameters` view, else a clean refusal naming the missing ids (this prevents the silent `RecipeError` stall the terminal-e2e feature documented); then the handshake (the availability request declaring `standard`) → verify the verbatim echo → publish **one batch containing exactly the scenarios not yet published** (the EDS tracks the published set in process memory) → consume the ack → report. An empty delta (nothing new to publish) is a clean refusal ("no new scenarios to publish"). Each Start runs the full retry skeleton fresh.
- `POST /api/finish` — only after a successful Start and before any finalization: publish the finalization batch, set `finalized`; a second Finish and any Start after finalization are refused (irrevocable).
- Errors are JSON `{error:"…"}` with a 4xx (client-side refusal) or 5xx (transport/DB failure) status.

### Publisher (the mock's proven skeleton — `test/mocks/eds/eds_mock.py` is the reference; D9)

Connect with bounded retries (default 60 attempts × 2s delay); the availability request with bounded retries (default 60 × 2s, per-request timeout 5s) — retry on timeout/transient transport errors; `status != "ready"`, a missing `batch_subject`, or a missing/mismatched echo is a **hard error** (no retry — surface the reason); publish to the reply's batch subject via JetStream; success requires the PubAck. The same skeleton applies to the finalization publish. No keep-alive loop beyond the HTTP service: the EDS is a long-running service, each action re-enters the skeleton (reuse the connection while healthy; reconnect with the retry skeleton on loss).

## Change

1. **config** — parse + validate the env set and the Secret (fail-fast, descriptive errors naming the exact field).
2. **wire** — the four payload types + validation helpers (non-empty project matching the identity, positive counts, the enum, the finite-positive metric); golden-JSON tests per the contract.
3. **recipestore** — the self-provision DDL, the entry/recipe upserts, the single-transaction save, the id assignment + 1–4 refusal, the view reads.
4. **server** — the API endpoints above + the embedded UI (one static page; no server-side templating) + the four-step flow state (designed → saved → started → finished) derived from the design state + the last handshake/publish results + the finalization flag — never a second source of truth.
5. **publisher** — the handshake + batch publish + finalization publish per the skeleton.
6. **main** — wire config → store → publisher → server; graceful shutdown.
7. **Dockerfile** — static linux binary, scratch base, `USER 1000:1000`, no secrets baked; mirror the PPS/translator layering.
8. **README** — component doc: purpose, the wire contract tables, the API surface, the env, the four-step flow incl. the Finish action, the self-provision behavior.
9. **go.work + the three licensed Makefile line-contacts** — per Target. `go build ./...` from the repo root must compile all modules; `make test-fast` must run your module's vet + race suite.
10. **Tests** — golden-JSON wire tests (every payload shape, the omitempty behavior, the echo-mismatch poison); config/Secret validation tests (every missing/malformed field); recipestore tests against a fake (the self-provision no-op on a pre-created schema, the id-assignment floor 5, the 1–4 write refusal, the single-transaction save with the entry upsert + recipe locator update, the view projection read); server tests via `httptest` (each endpoint; the pre-flight refusal with an unresolvable `parameterset_id`; the empty-delta refusal; the Finish irrevocability — a second Finish refused, a Start after Finish refused; the identity-mismatch poison); publisher tests against the fake-NATS pattern the sibling modules use (handshake success, the retry on timeout, the hard error on `status=error`, the echo check, the finalization publish, the PubAck gating); the UI-asset test (`GET /` serves the page).

## Constraints

- Mirror the PPS/translator structural and testing discipline; no `scenario-manager` imports (Go internal rules — duplicate the wire shapes per the normative contract, golden-tested).
- The Detail DB is the only store; your write surface is exactly the entry tables and `recipes` (never the view — it is a view; never ids 1–4; never any other schema or table). The Result DB is out of reach entirely.
- The EDS owns no Kubernetes credentials: no client-go, no in-cluster config; identity comes from env only.
- No harness edits; no cluster operations. `make test-fast` rc=0 is mandatory (with your licensed Makefile lines in place); **the cluster smoke is the wave gate**, run by the manager after the wave settles — you do not run `make test-smoke`.
- go.work discipline: no requirement bumps in the other modules' `go.mod` files (your `go.work`/`go.work.sum` edits must leave their builds byte-stable — prove with `cd scenario-manager && go build ./...` rc=0 and `cd experiment-operator && go build ./...` rc=0).
- **Parallel wave-mates:** E2 owns `experiment-operator/**`; E3 owns `scenario-manager/**`. Their uncommitted files will appear in `git status` — expected co-residency, never touch them. **Stage only your own partition** (explicit `git add <paths>` of your files — never bare `git add .`), because your commits interleave with the wave-mates' on the shared feature branch.
- **Commits:** your work lands on the feature branch (the worktree's branch, `D4NS3U/experimental-design-service`) — never `main`, never another worker's branch. Every commit carries the traceability trailer `[orchestration: task <task-id> dispatch <dispatch-id>]` (the IDs from your injected preamble). Commit at natural milestones; leave the branch in a test-green state at `worker_done`.
- Global constraints of FEATURE.md §5 apply verbatim (attestation, read-only specs, scope discipline, verification honesty, licensing headers).

## Ownership

Sole owner of the new module + `go.work`/`go.work.sum` + the three licensed Makefile line-contacts. Discovered gaps → report lines in `worker_done`, do not fix.

## Observable acceptance

Run and echo all of these in `worker_done` (the manager re-runs each independently):

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat; mismatch → stop, `--outcome failed`).
2. Containment: `git status --short` — your new module (untracked dir), `go.work`/`go.work.sum`, the Makefile, plus expected co-resident wave-mate files; zero files outside your partition.
3. `cd component-templates/experimental-design-service && go build ./... && go vet ./...` → rc=0.
4. `make test-fast` rc=0 final receipt (mandatory — includes your vet + race lines).
5. Targeted: `cd component-templates/experimental-design-service && go test -race -count=1 ./...` → rc=0.
6. Sibling byte-stability receipts: `cd scenario-manager && go build ./...` rc=0 and `cd experiment-operator && go build ./...` rc=0 — also echo `git diff --stat go.work` and the three Makefile diff lines.
7. Wire-proof receipts: echo the golden-JSON test names covering the four canonical payloads (availability request with declared `standard`; reply with the echo; scenario batch non-final (no `final` field); finalization batch `scenarios: []` + `final: true`; ack) and the echo-mismatch hard-error test name.
8. Store-proof receipts: echo the test names covering (a) the self-provision no-op on a pre-created schema, (b) the id 1–4 write refusal, (c) the next-id ≥ 5 assignment, (d) the single-transaction save (entry upsert + recipe locator).
9. API-proof receipts: echo the `httptest` names covering the pre-flight refusal (unresolvable `parameterset_id`), the empty-delta Start refusal, and the Finish irrevocability (second Finish refused; Start after Finish refused), plus the `GET /` UI-asset test.
10. Commit receipts: the branch HEAD, and `git log --format=%b -3 | grep 'orchestration:'` showing the trailer on your commits.

Completion protocol: `worker_done` exactly once, with a three-sentence executive summary, both lifecycle IDs (task + dispatch from your preamble), explicit `--outcome succeeded|failed`, the verbatim attestation line, the ten evidence blocks, and `--files-modified`. Read coordinator follow-ups at each natural checkpoint via `orca orchestration check --terminal <your_handle> --json`, and once more immediately before `worker_done`.

**Session hygiene (binding, learned from the PPS S1 crash):** keep tool outputs small (`head`/`tail`/`grep -n`, never whole-file cats of large files); gather evidence incrementally as you complete each requirement rather than batching everything into one giant end-session run.
