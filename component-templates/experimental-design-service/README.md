# CBSE Reference ExperimentalDesignService

The reference ExperimentalDesignService (EDS) is the per-experiment
component the CR already declares (`spec.experimentalDesignService`) and the
operator finally provisions (`reconcileEDS`). It lets the experimenter
**design the experiment interactively** and delivers the design into the
running system:

- a **small web UI** (served on the CR's port, fronted by the CR's Service)
  where the user designs the experiment: **parameter entries** (values of the
  simulation's four varyable parameters — `arrival_rate`, `service_rate`,
  `run_duration`, `seed_policy`) and the **scenarios** (each with
  `priority`, `number_of_reps`, `confidence_metric`, and a **recipe** — the
  explicit composition locating one entry row per parameter);
- an explicit **"Save Experiment"** action — the entries and the recipes
  into the experiment's Scenario Detail Database, one transaction;
- an explicit **"Start Experiment"** action — the pre-flight (every
  batch-referenced `parameterset_id` must resolve, else a clean UI-visible
  refusal), the landed availability handshake, and the scenario batch
  publish;
- an explicit **"Finish Experiment"** action — the finalization batch
  (`scenarios: []`, `final: true`); finalization is **irrevocable**.

The wire contract is the normative interface: the EDS is a new *producer* at
the existing Scenario Manager contract's edge. The embedded UI is a thin
Facade over the JSON API — every button maps to exactly one endpoint, and
the e2e drives the same API (the smoke's determinism is the API's
determinism).

This module is a standalone Go module
(`github.com/D4NS3U/cbse/component-templates/experimental-design-service`)
modeled on the reference PostProcessingService/Translator archetypes:
`config` / `wire` / `recipestore` / `publisher` / `server` packages, a
static scratch Dockerfile, and fake-based unit tests. It never imports
`scenario-manager/internal/**` (Go internal-package rules); its `wire`
types duplicate the shared contract field-for-field, byte-pinned by the
FEATURE.md §5 parallelism contract, and are golden-tested.

## Layout

```
cmd/experimental-design-service/   entrypoint (env + Secret → self-provision → serve)
internal/config/                   env + Secret parsing/validation, fail-fast startup
internal/wire/                     the four protocol payloads, strict unmarshal + domain validation,
                                   golden-tested against the byte-pinned contract
internal/recipestore/              the pgx design-layer writer: the six-object self-provision DDL,
                                   entry CRUD + recipe locator rows, single-transaction save,
                                   the id rules (>= 5; ids 1-4 refused), the view reads
internal/publisher/                the NATS connect → availability handshake → JetStream batch
                                   publish, the mock's bounded-retry skeleton
internal/server/                   the net/http JSON API + the embedded static UI page + the
                                   four-step flow state
```

## Runtime configuration

### Environment variables

Injected by the Operator; the EDS validates every value at startup and fails
fast on any missing, malformed, or inconsistent value (the batch subject is
never part of the configuration — it arrives from the SM's availability
reply at runtime).

| Variable | Value | Meaning |
|---|---|---|
| `NATS_URL` | e.g. `nats://sm-eds-nats:4222` | JetStream endpoint (no credentials in the URL) |
| `EDS_AVAILABILITY_SUBJECT` | `cbse.<ns>.<project>.eds.scenarios.available` | the per-project availability subject (operator-derived from the identity; an exact-match is enforced) |
| `SIMULATIONPROJECTNAMESPACE` | the downward-API namespace | the experiment's namespace |
| `SIMULATIONPROJECTNAME` | the downward-API project name | every payload's `project` field must equal this (a mismatch is poison) |
| `SIMULATIONEXPERIMENTUID` | the downward-API UID | the experiment UID |

### Container args

| Arg | Default | Meaning |
|---|---|---|
| `-listen` | `:8082` | the HTTP listen address (the CR's port) |

### The Detail DB connection Secret

The mounted `<exp>-detaildb-sct` Secret at `/detaildb-connection/`, with the
same five-field endpoint contract the Translator validates: keys `host`,
`port`, `dbname`, `user`, `password` (all present; port in 1..65535;
`sslmode=disable` is always set by the client).

## The wire contract (byte-pinned)

The four protocol payloads, pinned field-for-field (including the omitempty
behavior) against the Scenario Manager's landed structs plus the ruled
SBO additions:

- **Availability request** (the EDS publishes on
  `cbse.<ns>.<proj>.eds.scenarios.available`):
  `{"batch_id":"<id>","project":"<exp name>","scenario_count":N,"experiment_type":"standard"}`
  — the reference EDS always declares `experiment_type: "standard"`;
  `scenario_count` is 0 on the finalization handshake.
- **Availability reply** (the EDS consumes):
  `{"status":"ready"|"error","reason":"…","batch_subject":"…","experiment_type":"…"}`
  — the EDS requires `status=="ready"` and a non-empty `batch_subject`, and
  the **verbatim echo** `experiment_type == "standard"` (a missing or
  mismatched echo is a hard protocol error — a clean, UI-visible,
  retryable-on-the-next-Start failure indicating an SM without the
  finalization protocol).
- **Scenario batch** (the EDS publishes to the reply's batch subject):
  `{"batch_id":"<id>","project":"<exp name>","scenarios":[{"priority":N,"number_of_reps":N,"recipe_info":{"parameterset_id":N},"confidence_metric":<finite > 0>}]}`
  — `final` absent (a non-final batch marshals without the `final` field);
  `number_of_reps` in 1..100000, `priority` > 0.
- **Finalization batch** (the EDS publishes on Finish):
  `{"batch_id":"<id>","project":"<exp name>","scenarios":[],"final":true}`
  — the empty array is present, `final` is `true`.
- **Batch ack** (the EDS consumes):
  `{"status":"inserted"|"poison"|"error","batch_id":"<id>","received":N,"inserted":N,"failed":N,"reason":"…"}`
  — `status` + `reason` are surfaced in the API response; `inserted` is
  success.

### The publisher skeleton

The mock's proven bounded-retry skeleton (the `test/mocks/eds/eds_mock.py`
reference, ruling D9):

- connect with bounded retries: 60 attempts × 2s delay;
- the availability request with bounded retries: 60 × 2s, per-request
  timeout 5s — retried on timeout/transient transport errors;
- `status != "ready"`, a missing `batch_subject`, or a missing/mismatched
  echo is a **hard error** (no retry — the reason is surfaced);
- publish to the reply's batch subject via JetStream; success requires the
  PubAck.

The EDS is a long-running service with no keep-alive loop beyond the HTTP
service: each action re-enters the skeleton, reusing the connection while
it is healthy and reconnecting with the retry skeleton on loss.

## The API

The embedded UI is a thin Facade over this surface; the e2e drives the same
API. Errors are JSON `{"error":"…"}` with a 4xx (client-side refusal) or 5xx
(transport/DB failure); the clean SM refusal (the experiment not
`InProgress`) surfaces as a 409 carrying the SM's reason, a
protocol/transport failure (echo mismatch, missing batch subject, exhausted
retries, PubAck loss) as a 502.

| Endpoint | Action |
|---|---|
| `GET /` | the embedded UI: the state panel + the four action areas (Design / Save Experiment / Start Experiment / Finish Experiment); after a successful Finish the Start and Finish controls are disabled client-side (the server also enforces it) |
| `GET /api/state` | `{design:{entries,recipes,scenarios},flow:{saved,started,finalized,last_handshake,last_publish},db_rows}` — the design, the derived flow state, and the full `simulation_parameters` view projection (all rows, baseline included) |
| `POST /api/entries` | `{parameter, value}` → `{id}` — an in-memory assignment per the id rules (the value-domain check applies) |
| `POST /api/recipes` | `{arrival_rate_id, service_rate_id, run_duration_id, seed_policy_id}` (all must reference entries assigned in this process) → `{parameterset_id}` |
| `POST /api/scenarios` | `{priority > 0, number_of_reps 1..100000, confidence_metric finite > 0, parameterset_id}` (the recipe must be in this process) → appends an unpublished scenario |
| `DELETE /api/scenarios/<index>` | removes an **unpublished** scenario; a published one is refused (it already exists in the SM) |
| `POST /api/save` | the single-transaction save (entries + recipes) → `{saved_entries, saved_recipes}` |
| `POST /api/start` | the pre-flight (every `parameterset_id` must resolve in the view, else a clean refusal naming the missing ids), the handshake (declaring `standard`), the verbatim-echo check, and the publish of one batch containing exactly the scenarios not yet published; an empty delta is a clean refusal ("no new scenarios to publish"); each Start runs the full retry skeleton fresh |
| `POST /api/finish` | only after a successful Start and before any finalization: publish the finalization batch, set `finalized`; a second Finish and any Start after finalization are refused (irrevocable) |

## The Detail DB (the design layer)

The Detail DB is the EDS's only store; the write surface is exactly the
entry tables and `recipes`. At startup the EDS **self-provisions** the
canonical six objects (ruled R3): the four per-parameter entry tables
(`arrival_rate`, `service_rate`, `run_duration`, `seed_policy`), the
`recipes` locator table, and the `simulation_parameters` **view** over the
join — `CREATE TABLE IF NOT EXISTS` / `CREATE OR REPLACE VIEW` (the only
valid idempotent view form in PostgreSQL — `CREATE VIEW IF NOT EXISTS`
does not exist), the DDL
mirroring the reference seed SQL, a no-op against a pre-created matching
schema (both delivery branches work: the seeded reference image and a
user-pre-created host schema). No baseline INSERTs on the dynamic branch.

- **Id assignment:** per table, next id = `MAX(id)+1` with the floor 5 (an
  empty table, or one holding only the baseline 1–4, assigns 5). The EDS
  **never writes ids 1–4** in any of the five tables — an attempted write is
  a clean refusal.
- **Save semantics (one transaction):** upsert the design's entries (INSERT
  newly assigned ids; UPDATE the values of ids this process assigned — the
  live-locators ruling: a saved design's resolved values track its entries
  until the next save) and the recipe locator rows (INSERT new
  `parameterset_id`s; UPDATE the four FK columns of ids this process
  assigned).
- **Value domains** (mirroring the DDL CHECKs, refused at the API):
  `arrival_rate`/`service_rate`/`run_duration` positive integers;
  `seed_policy` integer ≥ 0.
- The `simulation_parameters` view keeps the Translator's landed lookup
  working unchanged (view-transparent).

The in-memory design state is the process's design: a restart starts with
an empty design (the ruled in-memory state); the saved rows remain in the
DB, and a fresh design gets fresh ids from 5 upward.

## Building

```
docker build --build-arg EDS_GO_BUILDER_IMAGE=<locked-digest> \
  -t <registry>/experimental-design-service:<tag> .
```

The builder argument is a locked Go toolchain image (linux/amd64 manifest
digest); a missing argument fails the build. The final stage is `scratch`
with only the static binary, running as `USER 1000:1000` — no secrets, no
credentials, no Python.
