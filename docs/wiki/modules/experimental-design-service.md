# Reference Experimental Design Service (EDS)

The per-experiment component declared by `spec.experimentalDesignService` on the
[SimulationExperiment](../diagrams/class-diagram.md) CR and provisioned by the
operator's `reconcileEDS`. It lets the experimenter design the experiment
interactively — parameter entries, recipes, scenarios — persist the design into
the experiment's Scenario Detail Database, and deliver it to the running
Scenario Manager over the alpha4 SBO wire. It is a standalone Go module
(`github.com/D4NS3U/cbse/component-templates/experimental-design-service`) with
five internal packages; it never imports `scenario-manager/internal/**` (Go
internal-package rules) — its `wire` package duplicates the shared contract
field-for-field, byte-pinned and golden-tested.

Source root: `component-templates/experimental-design-service/`

| Package | Responsibility |
|---|---|
| `cmd/experimental-design-service/` | entrypoint: config → Detail DB self-provision → NATS publisher → HTTP server, graceful shutdown on SIGINT/SIGTERM |
| `internal/config/` | environment + container arg + mounted-Secret parsing and fail-fast validation |
| `internal/wire/` | the four SBO protocol payloads, strict decode, domain validation, golden tests |
| `internal/recipestore/` | the pgx design-layer writer: six-object self-provision DDL, entry/recipe upserts, id assignment, view reads |
| `internal/publisher/` | the NATS connect → availability handshake → JetStream batch publish, with the mock's bounded-retry skeleton |
| `internal/server/` | the net/http JSON API, the embedded static UI, and the four-step flow state |

## Identity, configuration, and fail-fast startup

The EDS owns no Kubernetes credentials; its identity comes exclusively from the
downward-API environment. `config.Load`
([`internal/config/config.go`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/experimental-design-service/internal/config/config.go))
rejects any unknown flag, missing, malformed, or inconsistent value with a
descriptive error before any NATS connection or HTTP serving; `main` then exits
via `log.Fatalf`.

| Variable | Value / validation |
|---|---|
| `NATS_URL` | required; URL with a host; user information (credentials) in the URL is refused |
| `EDS_AVAILABILITY_SUBJECT` | required; must exactly equal the canonical subject `cbse.<namespace>.<project>.eds.scenarios.available` derived from the identity (free-form subjects are refused) |
| `SIMULATIONPROJECTNAMESPACE` | required; lowercase DNS label, 1–63 chars |
| `SIMULATIONPROJECTNAME` | required; DNS label; every published payload's `project` field must equal it (a mismatch is poison) |
| `SIMULATIONEXPERIMENTUID` | required; 8-4-4-4-12 lowercase hex |

Container arg: `-listen` (default `:8082`, the CR's Service port).

The Detail DB connection comes from the mounted `<exp>-detaildb-sct` Secret at
`/detaildb-connection/` with five files — `host`, `port`, `dbname`, `user`,
`password` — all non-empty, port in 1..65535. The client always connects with
`sslmode=disable` (the pgx v5 connector in
[`internal/recipestore/pgx.go`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/experimental-design-service/internal/recipestore/pgx.go)
builds the DSN from separate host/port fields and sets the password on the
parsed config, never in the DSN string). The batch subject is **never** part of
the configuration: it arrives from the Scenario Manager's availability reply at
runtime.

Startup order in
[`cmd/experimental-design-service/main.go`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/experimental-design-service/cmd/experimental-design-service/main.go):
load config → `store.EnsureSchema(ctx)` (ruled R3 self-provision, below) →
`publisher.New` → `server.New` → `ListenAndServe` with `ReadHeaderTimeout`
10 s; on SIGINT/SIGTERM the server shuts down with a 10-second context timeout.
Any failure before serving aborts the process.

## REST API surface (`internal/server`)

`App.ServeHTTP`
([`internal/server/server.go`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/experimental-design-service/internal/server/server.go))
is a single mux over eight API paths plus the embedded UI. Errors are JSON
`{"error": "..."}`: 4xx for client-side refusals (malformed input, flow-state
conflicts), 5xx for transport/DB failures. The clean SM refusal (the experiment
not `InProgress`) surfaces as a **409** carrying the SM's own reason; a
protocol/transport failure (echo mismatch, missing batch subject, exhausted
retries, PubAck loss) surfaces as a **502**.

| Endpoint | Request | Response | Behavior |
|---|---|---|---|
| `GET /` | — | the embedded `ui/index.html` (served with `Content-Type: text/html; charset=utf-8` from the `embed.FS`, no templating) | thin Facade over the API: every button maps to exactly one endpoint, and the e2e drives the same API |
| `GET /api/state` | — | `{design:{entries,recipes,scenarios}, flow:{saved,started,finalized,last_handshake?,last_publish?}, db_rows}` | `db_rows` is the full `simulation_parameters` view projection (all rows, baseline included); `scenarios` carry a zero-based `index` and a `published` flag |
| `POST /api/entries` | `{parameter, value}` | `{id}` | value-domain check (`ValueDomain`), then assigns the next EDS id (below) and appends the in-memory entry |
| `POST /api/recipes` | `{arrival_rate_id, service_rate_id, run_duration_id, seed_policy_id}` | `{parameterset_id}` | all four FKs must reference entries **assigned in this process** (per parameter); assigns the next `parameterset_id` |
| `POST /api/scenarios` | `{priority, number_of_reps, confidence_metric, parameterset_id}` | `{index}` | guards: `priority > 0`, `number_of_reps` in 1..100000 (`wire.MinReps`/`MaxReps`), `confidence_metric` finite and > 0, `parameterset_id` must reference a recipe in this process |
| `DELETE /api/scenarios/<index>` | — | `204 No Content` | removes an **unpublished** scenario; a published one is refused with 409 (it already exists in the Scenario Manager); unknown index is 404 |
| `POST /api/save` | — | `{saved_entries, saved_recipes}` | the single-transaction save into the Detail DB (below) |
| `POST /api/start` | — | `{batch_id, status, reason?, ack?, published, batch_subject}` | the pre-flight, the handshake, the publish of exactly the not-yet-published scenarios |
| `POST /api/finish` | — | `{batch_id, status, reason?}` | the finalization batch; only after a successful Start; irrevocable |

Request bodies are decoded strictly (`DisallowUnknownFields`, trailing tokens
rejected). `GET /api/state`'s `flow.saved` is `savedOnce && !dirty`, where
`dirty` is set by any entries/recipes mutation after the last successful save —
the flow state (designed → saved → started → finished) is derived from the
in-memory design state plus the last handshake/publish results plus the
finalization flag, never a second source of truth.

### The Start action

`handleStart` refuses with 409 when `finalized` (irrevocable) or when the
unpublished delta is empty ("no new scenarios to publish"). It then runs the
pre-flight: every batch-referenced `parameterset_id` must resolve in the
`simulation_parameters` view, else a clean 409 refusal naming the missing ids —
this prevents the scenario reaching the Translator's `RecipeError` path (a
silent stall). It calls `Publisher.Start`, which runs the full handshake +
publish skeleton fresh (each Start re-enters the retry skeleton). On success the
published scenarios flip to `Published`, `started` becomes true, and
`last_handshake`/`last_publish` record the outcome. Failure classification:
`publisher.ErrUnavailable` → 409 with the SM's reason recorded in
`last_handshake`; protocol/publish failures → 502 recorded in `last_publish`.

### The Finish action

`handleFinish` requires `started && !finalized` (409 otherwise), then calls
`Publisher.Finish`, which publishes the finalization batch
(`scenarios: []`, `final: true`). On success `finalized` is set — a second
Finish and any Start after finalization are refused (irrevocable). A clean SM
refusal is a 409; a protocol/publish failure is a 502.

### The embedded UI

`internal/server/ui/index.html` (285 lines) is the single static asset served at
`GET /`. It renders a flow-state panel (saved/started/finalized badges plus the
last handshake and publish results) and the four action areas — Design
(entries/recipes/scenarios forms), Save Experiment, Start Experiment, Finish
Experiment — each calling exactly one API endpoint. After a successful Finish the
Start and Finish controls are disabled client-side; the server enforces the same
rule.

## The SBO publisher wire (`internal/publisher`, `internal/wire`)

The publisher is a long-running service with no keep-alive loop beyond the HTTP
service: each action re-enters the skeleton, reusing the connection while it is
healthy and reconnecting with the retry skeleton on loss. The mock's proven
bounded-retry skeleton (ruling D9) provides the defaults:

- **connect** retries: 60 attempts × 2 s delay (`DefaultConnectAttempts`,
  `DefaultRetryDelay`);
- **availability request** retries: 60 × 2 s, per-request timeout 5 s
  (`DefaultRequestTimeout`);
- success requires the JetStream **PubAck** on the batch publish.

The real adapter is
[`internal/publisher/nats.go`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/experimental-design-service/internal/publisher/nats.go):
a Core-NATS `RequestMsg` (bounded by the per-request timeout) for the
handshake, a JetStream `js.Publish` (blocking for the PubAck) for the batch. The
publisher itself is transport-agnostic behind the `Conn`/`Dialer` seams, so the
skeleton is unit-tested with a fake NATS.

### The availability handshake

The EDS publishes an `AvailabilityRequest` on the per-project subject
`cbse.<namespace>.<project>.eds.scenarios.available`:

```json
{"batch_id":"eds-exp-1","project":"exp","scenario_count":2,"experiment_type":"standard"}
```

All four fields are always present on the wire. `scenario_count` is the number
of scenarios in the upcoming batch (0 on the finalization handshake). The
reference EDS always declares `experiment_type: "standard"`
(`wire.ExperimentTypeStandard`) — a capability handshake of the finalization
protocol, not a behavior selector.

The SM's `AvailabilityReply` is consumed from the request/reply:

```json
{"status":"ready","batch_subject":"cbse.ns.exp.eds.scenarios","experiment_type":"standard"}
```

`ReplyError` (in `internal/wire/wire.go`) classifies the consumed reply:
`status "ready"` requires a non-empty `batch_subject` (the exact per-project
batch subject `cbse.<namespace>.<project>.eds.scenarios`); `status "error"` is
the clean refusal with the SM's reason (the experiment is not `InProgress`);
and the reply must **echo `experiment_type` verbatim** — a missing or mismatched
echo is a hard protocol error indicating an SM without the finalization
protocol. Transient outcomes (timeout, transport error) retry within the
skeleton; a consumed-but-malformed reply is a hard error (the SM is reachable
and its reply is broken — surfacing beats retrying).

### Batch delivery

On a successful handshake the publisher builds one `ScenarioBatch` and
publishes it to the reply's batch subject via JetStream, gated on the PubAck.
Non-final batches marshal **without** the `final` field (omitempty); the
golden tests pin this byte-for-byte:

```json
{"batch_id":"eds-exp-1","project":"exp","scenarios":[{"priority":1,"number_of_reps":20,"recipe_info":{"parameterset_id":5},"confidence_metric":0.91}]}
```

The finalization batch carries the **present empty array** and `final: true`:

```json
{"batch_id":"eds-exp-2","project":"exp","scenarios":[],"final":true}
```

Each `ScenarioRecord` carries the per-scenario subset the EDS provides —
`priority`, `number_of_reps`, `recipe_info.parameterset_id` (the Detail DB
locator), `confidence_metric` — and the Scenario Manager fills the remaining
`scenario_status` columns on insert. Batch ids are process-scoped:
`eds-<project>-<n>` from a monotonically increasing `batchSeq`
(`NextBatchID`). Every batch is validated against the process identity before
publication: `project` must exactly equal the identity's project (a mismatch is
poison), and every record must satisfy `priority > 0`, `number_of_reps` in
1..100000, `parameterset_id > 0`, `confidence_metric` finite and > 0.

### Ack semantics

The SM's `ScenarioBatchAck` reports how many scenarios it persisted:

```json
{"status":"inserted","batch_id":"eds-exp-1","received":2,"inserted":2,"failed":0}
```

Status is one of `inserted` (success), `poison`, or `error` (the latter two
carry a `reason`). The reference EDS **publishes without a reply subject and
gates on the JetStream PubAck**, so a successful publish reports status
`"inserted"` and the `ack` object in the API response is present only when an
ack payload was observed for the batch. The seam is `publisher.SetAck` (a pure
decode + validation: an ack whose `batch_id` is present but mismatches the
published batch is poison); today the Scenario Manager answers on the delivery's
reply subject only when one is present, so the EDS's PubAck is its confirmation
and the seam is exercised by the tests. On the SM side, a non-final batch
arriving after finalization is rejected without insertion with the pinned ack
`{"status":"error", ..., "reason":"experiment finalized; no further batches
accepted"}` (`communication.ReasonExperimentFinalized` in
[`scenario-manager/internal/communication/communication.go`](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/communication/communication.go));
a final batch inserts its scenarios (if any) and then durably finalizes the
project.

The three publisher error types drive the API's error taxonomy:
`ErrUnavailable` (clean SM refusal → 409), `ErrProtocol` (hard handshake error,
echo mismatch, exhausted request retries → 502), `ErrPublish` (PubAck not
confirmed after a successful handshake → 502).

## The design layer (`internal/recipestore`)

The store is the EDS's only write surface: it writes exactly the entry tables
and `recipes`, never the view (it is a view), never ids 1–4, and never any other
schema or table. The Result DB is out of reach entirely.

### The six-object seed and the R3 self-provision

The canonical six objects — the four per-parameter entry tables, the `recipes`
locator table, and the `simulation_parameters` view — are pinned in
[`component-templates/scenario-detail-database/10-simulation-parameters.sql`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/scenario-detail-database/10-simulation-parameters.sql)
for the reference seeded image and restated verbatim as `canonicalDDL` in
[`internal/recipestore/recipestore.go`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/experimental-design-service/internal/recipestore/recipestore.go):

```sql
CREATE TABLE IF NOT EXISTS public.arrival_rate (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));
CREATE TABLE IF NOT EXISTS public.service_rate (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));
CREATE TABLE IF NOT EXISTS public.run_duration (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value > 0));
CREATE TABLE IF NOT EXISTS public.seed_policy  (id INTEGER PRIMARY KEY CHECK (id > 0), value INTEGER NOT NULL CHECK (value >= 0));
CREATE TABLE IF NOT EXISTS public.recipes (
    parameterset_id INTEGER PRIMARY KEY CHECK (parameterset_id > 0),
    arrival_rate_id INTEGER NOT NULL REFERENCES public.arrival_rate(id),
    service_rate_id INTEGER NOT NULL REFERENCES public.service_rate(id),
    run_duration_id INTEGER NOT NULL REFERENCES public.run_duration(id),
    seed_policy_id  INTEGER NOT NULL REFERENCES public.seed_policy(id)
);
CREATE OR REPLACE VIEW public.simulation_parameters AS
    SELECT r.parameterset_id, a.value AS arrival_rate, s.value AS service_rate,
           d.value AS run_duration, p.value AS seed_policy
    FROM public.recipes r
    JOIN public.arrival_rate a ON a.id = r.arrival_rate_id
    JOIN public.service_rate  s ON s.id = r.service_rate_id
    JOIN public.run_duration  d ON d.id = r.run_duration_id
    JOIN public.seed_policy   p ON p.id = r.seed_policy_id;
```

`Store.EnsureSchema` (ruled R3) runs exactly these statements at startup,
before serving. Against a pre-created matching schema (the seeded reference
image, or a user-pre-created host schema) every statement is idempotent: the
tables by `CREATE TABLE IF NOT EXISTS`, the view by `CREATE OR REPLACE VIEW` —
the only valid idempotent view form in PostgreSQL, since
`CREATE VIEW IF NOT EXISTS` does not exist. The dynamic branch performs **no
baseline INSERTs**: it is a no-op against the seeded image.

The seeded image's immutable baseline (ids 1–4, read-only in all five tables;
the EDS id-space starts at 5) is:

| id | arrival_rate | service_rate | run_duration | seed_policy |
|---|---|---|---|---|
| 1 | 2 | 4 | 100 | 1001 |
| 2 | 3 | 5 | 120 | 1002 |
| 3 | 4 | 7 | 150 | 1003 |
| 4 | 5 | 8 | 180 | 1004 |

with `recipes` rows `(parameterset_id, arrival_rate_id, service_rate_id,
run_duration_id, seed_policy_id)` = `(1,1,1,1,1)`, `(2,2,2,2,2)`, `(3,3,3,3,3)`,
`(4,4,4,4,4)`. The view therefore projects the legacy rows
`(1,2,4,100,1001)`, `(2,3,5,120,1002)`, `(3,4,7,150,1003)`, `(4,5,8,180,1004)`.

### Recipe state, ids, and the monotonic ceilings

A **recipe** is one `recipes` locator row: `parameterset_id` plus one FK per
parameter (`arrival_rate_id`, `service_rate_id`, `run_duration_id`,
`seed_policy_id`) locating the entry rows a scenario resolves against. An
**entry** is one row in one of the four per-parameter tables, addressed by its
EDS-assigned id.

The store derives the next id from persisted rows only:

- `NextID(parameter)`: `SELECT GREATEST(COALESCE(MAX(id), 0) + 1, 5) FROM
  public.<table>` — an empty table, or one holding only the baseline 1–4,
  assigns 5;
- `NextRecipeID()`: the same statement over `recipes.parameterset_id`.

The server layers a **monotonic per-process ceiling** on top (settlement
erratum 6): `App.nextEntryID`/`App.nextRecipeID` return
`max(dbNext, issuedEntryID[parameter]+1)` / `max(dbNext, issuedRecipeID+1)` and
advance the ceiling (`issuedEntryID` is per-parameter, `issuedRecipeID`
process-wide) so two unsaved entries of one parameter cannot collide on one id
(the canonical multi-recipe flow: design the whole experiment, then Save). After
a Save persists rows, the DB-derived next id re-seeds the ceiling upward.
Any write targeting ids 1–4 is refused with `ErrReservedID` ("ids 1–4 are the
read-only reference baseline; the EDS id-space starts at 5").

### Save semantics (one transaction)

`Save` pre-validates every write (parameter table, `ValueDomain`, id ≥ 5)
before opening the transaction, then in one `BEGIN ... COMMIT`:

- entries: `upsertEntry` — `SELECT 1` existence probe per row, then `INSERT
  (id, value)` for new ids or `UPDATE ... SET value` for ids this process
  assigned (the live-locators ruling: a saved design's resolved values track
  its entries until the next save);
- recipes: `upsertRecipe` — `INSERT (parameterset_id, ...)` for new
  `parameterset_id`s or `UPDATE` of the four FK columns for ids this process
  assigned.

`ValueDomain` mirrors the DDL CHECKs at the API: `arrival_rate`, `service_rate`,
`run_duration` are positive integers; `seed_policy` is an integer ≥ 0.
`ViewRows` returns the full `simulation_parameters` projection ordered by
`parameterset_id` (the API's `db_rows`); `Resolve(ids)` reports which of the
given `parameterset_id`s do **not** resolve in the view (the Start pre-flight),
deduplicated in requested order.

The in-memory design state is the process's design (the ruled in-memory state):
a restart starts with an empty design, while saved rows remain in the Detail
DB and a fresh design gets fresh ids from 5 upward.

## Image and security

The
[Dockerfile](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/experimental-design-service/Dockerfile)
requires the locked `EDS_GO_BUILDER_IMAGE` build arg (Go toolchain,
linux/amd64 manifest digest; a missing argument fails the build), compiles a
static, stripped binary, and copies only that binary into a `scratch` final
stage running as `USER 1000:1000`. No secrets, no credentials, no Python; the
image carries no CA bundle (plain `nats://` and `sslmode=disable`
PostgreSQL, no outbound HTTPS verification).
