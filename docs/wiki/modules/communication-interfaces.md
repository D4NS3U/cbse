# Communication Interfaces

This page is the normative reference for every seam between CBSE components at `alpha4`: the NATS subject map and its payloads, the JetStream streams and durable consumers, the database contracts (Core DB, Scenario Detail DB, Result DB), and the `SimulationExperiment` CR as the control-plane interface. All names below are taken verbatim from source at the merge commit; links point into the repository.

## Subject grammar

Every subject has the fixed form `cbse.<namespace>.<project>.<domain>.<event>`, where `<namespace>` and `<project>` are each a single lowercase DNS label (1–63 chars, no dots) and the remaining tokens are reserved names. Identifiers are never normalized — a subject tokenizes to a fixed, unambiguous sequence, and the parser ([`subject.Parse`](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/subject/subject.go)) rejects any subject that does not match, so malformed subjects are permanent poison (ACK, no mutation). The three domains are `eds`, `trans`, and `pps`; the Translator-ready and PPS-evaluation subjects carry the per-scenario id as their fourth token.

## NATS subject map

| Subject | Stream / transport | Publisher | Subscriber | Payload (exact JSON) | Reply semantics |
| --- | --- | --- | --- | --- | --- |
| `cbse.<ns>.<proj>.eds.scenarios.available` | Core NATS (no stream) | EDS (reference, mock, e2e) | SM wildcard queue subscription `cbse.*.*.eds.scenarios.available`, queue group `scenario-manager-eds` | Request `{batch_id, project, scenario_count, experiment_type}` → reply `{status: "ready"\|"error", reason?, batch_subject?, experiment_type?}` | Request/reply, bounded by the EDS per-request timeout (5 s default) |
| `cbse.<ns>.<proj>.eds.scenarios` | JetStream `cbse_eds_scenarios` | EDS | SM durable `scenario-manager-eds-consumer` (queue group `scenario-manager-eds`, filter `cbse.*.*.eds.scenarios`) | `{batch_id, project, scenarios: [{priority, number_of_reps, recipe_info, confidence_metric}], final?}` | Fire-and-forget into a WorkQueue stream; ack to the SM is the JetStream delivery ACK/NAK; an optional `ScenarioBatchAck` rides the delivery's reply subject only when one is set (see below) |
| `cbse.<ns>.<proj>.trans.request` | JetStream `cbse_translator` | SM (selection loop) | Per-experiment durable `translator-<uid12>`, filter the exact request subject, `MaxAckPending 1` | `{id, translation_attempt, recipe_info, confidence_metric}` | Fire-and-forget; durable PubAck gates the SM's `MarkScenarioTranslationRequestPublished`; request ACKed only after the ready message is durably published |
| `cbse.<ns>.<proj>.trans.<scenario-id>.ready` | JetStream `cbse_translator` | Translator | SM durable `scenario-manager-translator-ready` (queue group `scenario-manager-translator-ready`, filter `cbse.*.*.trans.*.ready`) | `{translation_attempt, container_image}` (`container_image` is a digest reference or empty on a confirmed failure) | Fire-and-forget; PubAck gates the Translator's request ACK |
| `cbse.<ns>.<proj>.pps.request` | JetStream `cbse_pps` | SM (evaluation-publication loop) | Per-experiment durable `pps-<uid12>` — created by the SM (`EnsurePPSConsumer`), bound (never created) by the PPS, `MaxAckPending 1` | `{experiment_uid, namespace, project, scenario_id, runner_round, number_of_reps, confidence_metric}` — all fields required | Fire-and-forget; durable PubAck gates the SM's `MarkEvaluationRequestPublished`; request NAKed on Result DB or verdict-publication failure, ACKed on poison |
| `cbse.<ns>.<proj>.pps.<scenario-id>.evaluation` | JetStream `cbse_pps` | PPS | SM durable `scenario-manager-pps-evaluation` (queue group `scenario-manager-pps-evaluation`, filter `cbse.*.*.pps.*.evaluation`) | `{experiment_uid, namespace, project, scenario_id, runner_round, metric, verdict, sample_mean, half_width, replications, confidence_metric, additional_runners, max_replications}` — all 13 fields required | Fire-and-forget; PubAck gates the PPS's request ACK |

`<uid12>` is the first 12 lowercase hex characters of the experiment UID with hyphens removed ([`UIDPrefix`](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/nats/config.go)); it is shared by the per-experiment consumer names and the deterministic runner Job name `simrun-<uid12>-s<scenario-id>-a<attempt>[-r<round>]`.

### Streams

All three streams are reconciled by the SM at startup (`ReconcileStreamsAndConsumers`, additive subject union — existing subjects are never removed) with identical retention policy: `WorkQueuePolicy`, `FileStorage`, `DiscardOld` ([config](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/nats/config.go)):

| Stream | Subjects |
| --- | --- |
| `cbse_eds_scenarios` | `cbse.*.*.eds.scenarios` |
| `cbse_translator` | `cbse.*.*.trans.request`, `cbse.*.*.trans.*.ready` |
| `cbse_pps` | `cbse.*.*.pps.request`, `cbse.*.*.pps.*.evaluation` |

### Consumers

| Consumer | Owner | Filter | Key settings |
| --- | --- | --- | --- |
| `scenario-manager-eds-consumer` (queue group `scenario-manager-eds`) | SM | `cbse.*.*.eds.scenarios` | explicit ACK, DeliverAll, AckWait 2 m, MaxAckPending 1024, MaxDeliver −1, deliver subject `cbse.sm.eds-batch.deliver` |
| `scenario-manager-translator-ready` (queue group `scenario-manager-translator-ready`) | SM | `cbse.*.*.trans.*.ready` | as above, deliver subject `cbse.sm.translator-ready.deliver` |
| `scenario-manager-pps-evaluation` (queue group `scenario-manager-pps-evaluation`) | SM | `cbse.*.*.pps.*.evaluation` | as above, deliver subject `cbse.sm.pps-evaluation.deliver` |
| `translator-<uid12>` | per experiment | exact `cbse.<ns>.<proj>.trans.request` | explicit ACK, DeliverAll, AckWait 2 m, **MaxAckPending 1**, MaxDeliver −1, four ownership metadata entries |
| `pps-<uid12>` | per experiment (SM-created) | exact `cbse.<ns>.<proj>.pps.request` | identical settings to the Translator consumer, metadata `managed-by: pps` |

The three SM deliver subjects are 4-token internal push subjects that match no stream filter (a stream subject is 5 or 6 tokens), so delivered messages never re-enter a stream; the queue group distributes each delivery among SM replicas. The per-experiment consumers carry four ownership metadata keys (`experiment.cbse.terministic.de/managed-by`, `-experiment-uid`, `-namespace`, `-project`); ownership verification at deletion time requires all four plus durable name, filter, and settings to match exactly, and any mismatch is a collision that retains the finalizer ([consumers](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/nats/consumers.go), [Translator-side mirror](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/translator/internal/messaging/messaging.go), [PPS-side mirror](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/post-processing-service/internal/messaging/messaging.go)). Asymmetry: the Translator creates-or-attaches its own durable; the PPS only binds to the SM-created one — a missing PPS consumer is retried at 10 s (`ErrConsumerNotReady`), a mismatched one fails PPS startup.

### Delivery decision mapping

Both SM JetStream consumers map one decision per delivery ([EDS batch](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/nats/edsbatch.go), [ready](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/nats/readyconsumer.go), [PPS evaluation](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/nats/ppsevaluationconsumer.go)):

- **ACK (permanent poison)**: invalid subject shape, invalid identity tokens, subject/payload mismatch, malformed JSON (unknown fields or trailing tokens rejected by the strict decoders), out-of-domain values (e.g. `number_of_reps` outside `1..100000`, unknown `verdict` enum). No mutation.
- **ACK (terminal experiment)**: the lifecycle gate returns `AdmitTerminal` (phase `Error`/`Failed`/`Finished`, deletion begun, or CR gone) — ACK-and-discard.
- **NAK (retry)**: unavailable experiment (`Pending`/`Provisioning`), or a transient dependency failure (DB/K8s lookup) — redelivered, `MaxDeliver` unlimited.
- The semantic handler then decides: `Handled → ACK`, `Retry → NAK` ([ready workflow](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/ready/ready.go), [verdict workflow](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/verdict/verdict.go)).

## ScenarioBatchAck reply-subject gating

`ScenarioBatchAck` is `{status, batch_id?, received, inserted, failed, reason?}` with the three-value status enum `inserted | poison | error` ([communication package](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/communication/communication.go)). The landed SM behavior (completed by the finalization protocol, E3):

- An ack is **built for every admitted batch** and delivered on the batch delivery's **reply subject only when one is present** (`msg.Reply != ""`); a zero-value ack (empty `status`) means no delivery.
- The outcomes that actually deliver: `status: "inserted"` (received = decoded scenario count, inserted = persisted rows) and `status: "error"` with the pinned reason `experiment finalized; no further batches accepted` (a non-final batch arriving after finalization — permanent, so the delivery is ACKed once the EDS has its answer). Poison and gate outcomes keep the landed silent-discard behavior (zero-value ack, no reply).
- **The reference EDS, the protocol-faithful mock, and the e2e harness all publish without a reply subject and gate on the JetStream PubAck** — for them the ack reply is a no-op; the observable outcome of a publish is the `scenario_status` rows and the CR state, not an ack payload on the wire ([respondBatchAck](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/nats/edsbatch.go#L201), [EDS `SetAck` seam](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/experimental-design-service/internal/publisher/publisher.go#L368), [settlement erratum 1](https://github.com/D4NS3U/cbse/blob/c993397/devlog/changes/experimental-design-service/FEATURE.md)).
- JetStream delivery is at-least-once and the Core DB does not deduplicate `batch_id`, so redelivered final batches **with scenarios** can re-insert rows in the transient insert-then-mark window (documented limitation, erratum 3); the finalization-only shape (`scenarios: []`) is redelivery-safe because `MarkFinalized` is `INSERT ... ON CONFLICT DO NOTHING`.

## The Finish handshake

The finalization protocol is ruled in the [EDS FEATURE.md](https://github.com/D4NS3U/cbse/blob/c993397/devlog/changes/experimental-design-service/FEATURE.md) and landed on both sides:

**EDS side** ([publisher](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/experimental-design-service/internal/publisher/publisher.go)): `POST /api/finish` is allowed only after a successful Start and before any finalization. It re-enters the bounded-retry skeleton (connect 60 × 2 s; availability request 60 × 2 s, per-request timeout 5 s) with `scenario_count: 0` and `experiment_type: "standard"`, then publishes the finalization batch `{batch_id, project, scenarios: [], final: true}` to the reply's `batch_subject` (PubAck-gated). The server enforces the once-only rule: a second Finish and any Start after finalization are refused with 409 ("finalization is irrevocable").

**SM side** ([EDSBatchConsumer](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/nats/edsbatch.go)): a `final: true` batch inserts its scenarios, if any, then durably marks the project finalized — a row in `experiment_finalization` (`project_id` PK referencing `project.id` ON DELETE CASCADE, `finalized_at TIMESTAMP NOT NULL DEFAULT NOW()`), so the marker survives SM restarts. A non-final batch arriving after finalization is rejected without insertion with the pinned error ack.

**Detection (capability handshake)**: the availability request's `experiment_type` is echoed **verbatim and status-independently** on the SM's reply (ready *and* error); absent declaration ⇒ no echo ⇒ no check (the legacy exchange stays byte-identical). The reference EDS treats a missing or mismatched echo as a **hard, non-retried protocol error** (an SM without the finalization protocol), surfaced to the UI rather than burned into the retry budget.

**Verdict rule** ([`verdictForCounts`](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/aggregate/scheduler.go#L265)): any scenario `Failed` → experiment `Failed` fail-fast (finalization is irrelevant); else `Finished` iff `finalized && (total == 0 || finished == total)` — including the explicit zero-scenario Finish, which closes the empty interactive window, distinct from an absent-EDS experiment idling in `InProgress`. While all scenarios are terminal but the final batch is outstanding, the aggregate pass writes the diagnostic `status.message = "awaiting EDS finalization"` (merge patch, write-if-different, never `phase`, never the verdict); the operator's terminal derivation overwrites the message at the phase transition, which always lands later because `Finished` requires the finalization.

## Database contracts

### Core DB

The shared stack's PostgreSQL (a per-namespace `core-db` Deployment alongside the operator, SM, and NATS; the SM connects to the `scenarios` database via `SCENARIO_MANAGER_CORE_DB_DSN`) is owned at the schema level by the Scenario Manager ([`EnsureSchema`](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/persistence/schema.go), no ALTER repair — an incompatible table fails startup). Experiment state is per-project rows; three tables:

- `project` — exactly three columns: `id SERIAL PRIMARY KEY`, `project_namespace TEXT NOT NULL`, `project_name TEXT NOT NULL`, plus `UNIQUE (project_namespace, project_name)`. No status, no experiment-UID column.
- `scenario_status` — one row per scenario; `id SERIAL PRIMARY KEY`, `project_id` single-column FK `→ project.id ON DELETE CASCADE`, `state TEXT NOT NULL` with the seven values `Created | Scheduled | StartingRunners | InProcessing | PostProcessing | Finished | Failed` ([state constants](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/persistence/db.go#L141)), `created_at`/`updated_at TIMESTAMPTZ`, `priority`, `number_of_reps`, `number_of_computed_reps`, `translation_attempts`, `translation_publish_started_at`, `translation_request_published_at`, `recipe_info JSONB`, `container_image TEXT`, `confidence_metric DOUBLE PRECISION`, and the round bookkeeping / evaluation-publication-guard columns `runner_round`, `round_reps`, `round_computed_reps`, `evaluation_attempts`, `evaluation_publish_started_at`, `evaluation_request_published_at`.
- `experiment_finalization` — one row per finalized project: `project_id INTEGER PRIMARY KEY REFERENCES project(id) ON DELETE CASCADE`, `finalized_at TIMESTAMP NOT NULL DEFAULT NOW()`.

Writers are all SM workflows behind guarded transitions: batch intake (`InsertScenarioBatch`, one transaction per batch, registers the `project` row), the selection loop (`Created → Scheduled` + publish guards), the ready workflow (`Scheduled → StartingRunners` + `container_image`, or the empty-image attempt-consumption recovery), runnerstart (`StartingRunners → InProcessing`), observation (`InProcessing → PostProcessing` recording `number_of_computed_reps`, or `→ Failed`), the verdict workflow (`PostProcessing → Finished | StartingRunners` (new round) `| Failed`), evaluationpub (PostProcessing claim + publish guards), and `MarkFinalized`. The operator and components never touch this DB — it is SM-side (the operator holds no Core DB endpoint). Full state-machine detail: [modules/scenario-manager.md](scenario-manager.md).

### Scenario Detail DB

Per-experiment PostgreSQL (reference image or user host, selected by `spec.detailDatabase.image` vs `host`). The schema is the canonical six objects in the `public` schema, pinned in the reference seed ([`10-simulation-parameters.sql`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/scenario-detail-database/10-simulation-parameters.sql)) and self-provisioned verbatim by the EDS at startup ([`recipestore.EnsureSchema`](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/experimental-design-service/internal/recipestore/recipestore.go) — `CREATE TABLE IF NOT EXISTS` for the tables, `CREATE OR REPLACE VIEW` for the view, the only valid idempotent view form in PostgreSQL):

| Object | Shape | Writer | Reader |
| --- | --- | --- | --- |
| `arrival_rate` | `id INTEGER PRIMARY KEY CHECK (id > 0)`, `value INTEGER NOT NULL CHECK (value > 0)` | EDS only (ids ≥ 5) | EDS (view), Translator (via view) |
| `service_rate` | same shape | EDS only | as above |
| `run_duration` | same shape | EDS only | as above |
| `seed_policy` | `id INTEGER PRIMARY KEY CHECK (id > 0)`, `value INTEGER NOT NULL CHECK (value >= 0)` | EDS only | as above |
| `recipes` | `parameterset_id INTEGER PRIMARY KEY CHECK (> 0)` + four NOT NULL FKs (`arrival_rate_id`, `service_rate_id`, `run_duration_id`, `seed_policy_id`) | EDS only | EDS (pre-flight `Resolve`), Translator (via view) |
| `simulation_parameters` (view) | `SELECT r.parameterset_id, a.value AS arrival_rate, s.value AS service_rate, d.value AS run_duration, p.value AS seed_policy FROM recipes r JOIN …` | never (it is a view) | EDS (`GET /api/state` `db_rows`), Translator lookup |

- **EDS designs** through its API: `POST /api/save` upserts entries and recipe locator rows in one transaction (live locators — a saved design's resolved values track its entries until the next save); ids 1–4 are the read-only reference baseline in all five tables and any write targeting them is refused (`ErrReservedID`); EDS-assigned ids are `MAX(id)+1` with floor 5 per table.
- **Translator reads** through the single predefined parameterized query `SELECT arrival_rate, service_rate, run_duration, seed_policy FROM public.simulation_parameters WHERE parameterset_id = $1` — view-transparent, 30 s statement timeout, one cancellable 30 s delayed retry on transient failure (SQLSTATE classes 08/40/53, 57P01–57P03); a missing or multi-row `parameterset_id` is a permanent failure and flows to the Translator's empty-image workflow ([lookup](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/translator/internal/detaildb/detaildb.go)).
- **The runner never touches the Detail DB**: parameters are looked up once at generation time and baked into the image as `scenario.json`; Detail DB credentials are never baked into the generated image (only Result DB credentials are, the trusted-prototype exception) ([generator](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/translator/internal/generator/generator.go)).

### Result DB

Per-experiment PostgreSQL whose schema is entirely the runners' concern — no pre-created tables. For each scenario the generated runner creates `scenario_<scenario-id>_results` (`id BIGSERIAL PRIMARY KEY`, `result JSONB NOT NULL`) under `pg_advisory_xact_lock(0, <scenario-id>)` in a **single transaction** together with `SET statement_timeout = 30000` and the `INSERT` (the lock + create + insert share one transaction because psycopg 3 tears the connection down between successive explicit transactions on the locked PostgreSQL 18.6 image; the transaction lock still serializes concurrent first-use table creation for the same scenario) ([runner resultdb client](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/translator/internal/generator/runnermod/resultdb.py)). Any DB failure exits the pod non-zero (a failed Job pod → `InProcessing → Failed`).

- **Writer: the generated runner only.** One row per completed runner pod: a JSONB record with exactly `parameterset_id, arrival_rate, service_rate, run_duration, seed_policy, effective_seed, completed_customers, mean_wait_time` (no `scenario_id`, no credentials) ([result record](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/translator/internal/generator/runnermod/result.py)). Rows pool across rounds — round 2's pods append to the same table.
- **Reader: the PPS only.** `SELECT result FROM public.scenario_<id>_results` with a 30 s statement timeout; finite `mean_wait_time` values are extracted, malformed rows skipped and counted; an empty table is a zero-row result, not an error. The PPS is read-only here and never touches the Core DB ([PPS fetch](https://github.com/D4NS3U/cbse/blob/c993397/component-templates/post-processing-service/internal/resultdb/resultdb.go)).
- **Creator: the runner** (advisory-lock CREATE IF NOT EXISTS on first use). The SM creates nothing in this DB and connects to it nowhere.

## The SimulationExperiment CR as the control-plane interface

The CR ([types](https://github.com/D4NS3U/cbse/blob/c993397/experiment-operator/api/alpha4/simulationexperiment_types.go)) is spec-in, status-out.

**Spec (who writes: the user).** All required: `detailDatabase` and `resultDatabase` (each `DatabaseSpec`: exactly one of `image` | `host`, plus `dbname`, `user`, `password`, `port`, optional `serviceType`/`nodePort`/`command`/`args` — both immutable via CEL); `translator` (`image`, `repository`, `baseimage`, `builderImage`, `registryAuthSecretRef` — name must equal `cbse-registry-auth`, optional `builderResources` (CPU/memory only), `serviceType`, `nodePort`, `port` default 8080, `command`, `args` — all immutable); `postProcessingService` and `experimentalDesignService` (`image`, `design`, `serviceType`, `nodePort`, `port` default 8080, `command`, `args`); optional `runner.jobTemplate` (immutable `batchv1.JobTemplateSpec`, validated by the operator, consumed by the SM's runnerstart); `defaultServiceType` (ClusterIP default). `metadata.name` must be a lowercase DNS label ≤ 63 chars (CEL) — it is the `<project>` token in every subject.

**Status (field-disjoint ownership).**

| Field | Written by | Semantics |
| --- | --- | --- |
| `status.phase` | Experiment Operator only | enum `Pending; Provisioning; InProgress; Completed; Failed; Error; Finished`; `Finished`/`Failed` are the terminals, derived solely from `scenarioManagerVerdict`; sticky (the reconciler parks terminal phases) |
| `status.message` | Experiment Operator, except one SM case | human-readable detail; the SM's aggregate pass may merge-patch `message = "awaiting EDS finalization"` while InProgress with all scenarios terminal and finalization outstanding (write-if-different, never phase, never the verdict); the operator overwrites it at the terminal transition |
| `status.metrics.scenarioCount` | declared (`StatusMetrics`) but not populated by either writer in the merged tree | reserved count-based progress field |
| `status.scenarioManagerVerdict` | Scenario Manager only | enum `Finished; Failed`; absorbing (write-if-absent — once written it never changes); absent means "not yet reported", never user input |

The two writers' merge patches are field-disjoint by construction and enforced by landed tests: the SM verdict patch carries only `scenarioManagerVerdict`; the SM diagnostic patch carries only `message`; the operator's terminal derivation writes only `phase`/`message` and never writes the verdict ([SM patches](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/aggregate/store.go#L111), [operator derivation](https://github.com/D4NS3U/cbse/blob/c993397/experiment-operator/internal/controller/simulationexperiment_alpha4_controller.go#L297)).

**Deletion as an interface.** Deleting the CR triggers the SM finalizer cleanup in a fixed order: delete ownership-verified runner Jobs → verify-and-delete `translator-<uid12>` → purge `cbse.<ns>.<proj>.eds.scenarios` (stream `cbse_eds_scenarios`) → purge `trans.request` and `trans.*.ready` (stream `cbse_translator`) → delete the `project` row (cascades scenarios + finalization marker) → verify Job absence → remove the finalizer → purge `pps.request` and `pps.*.evaluation` (stream `cbse_pps`) → verify-and-delete `pps-<uid12>`. Every step is idempotent (missing = success); any collision or failure retains the finalizer for retry on the fixed cleanup cadence ([cleanup](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/lifecycle/cleanup.go), [NATS deletion client](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/internal/nats/nats_adapter.go)).

## Related pages

- [architecture.md](../architecture.md) — system shape, master diagram, numbered data-flow walkthrough
- [diagrams/sequences.md](../diagrams/sequences.md) — end-to-end sequence diagrams for the design flow, an execution round, and finalization
- [modules/scenario-manager.md](scenario-manager.md), [modules/experimental-design-service.md](experimental-design-service.md), [modules/translator.md](translator.md), [modules/post-processing-service.md](post-processing-service.md), [modules/scenario-databases.md](scenario-databases.md), [modules/simulation-runner.md](simulation-runner.md)
