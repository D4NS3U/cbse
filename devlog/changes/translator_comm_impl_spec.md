# Translator-Scenario Manager Communication

## Purpose

This document is the implementation-ready specification for the Scenario Manager (SM) to Translator handshake over NATS/JetStream.

Scope of this spec:
- selecting scenarios that are ready for translation
- publishing translation requests
- consuming Translator ready messages
- updating scenario state in the core database
- handling retries, duplicates, and stale work

Out of scope:
- Translator internal processing logic
- runner startup after a scenario becomes `Scheduled`
- heartbeat protocol between SM and Translator

## Target Files

Primary implementation locations:
- `scenario-manager/internal/nats/trans_com.go`
- `scenario-manager/internal/core/selector.go`
- `scenario-manager/internal/core/scenario_manager.go`
- `scenario-manager/internal/coredb/scenario_status.go`
- `scenario-manager/internal/coredb/schema.go`

## Required Schema Changes

The current `scenario_status` table is not sufficient for robust claiming and stale-work recovery. Add these columns:

| Column | Type | Purpose |
| --- | --- | --- |
| `created_at` | `TIMESTAMPTZ NOT NULL DEFAULT NOW()` | creation timestamp |
| `updated_at` | `TIMESTAMPTZ NOT NULL DEFAULT NOW()` | last state transition time |
| `translation_attempts` | `INTEGER NOT NULL DEFAULT 0` | retry counter |

`updated_at` must be set on every scenario state transition handled by SM.

## Canonical Scenario States

Use exactly these state strings:

1. `Created`
2. `Scheduled`
3. `StartingRunners`
4. `InProcessing`
5. `InPreProcessing`
6. `Finished`
7. `Failed`

Notes:
- Replace the current default state `Pending` with `Created`.
- Do not use spaces in state names.
- The canonical lifecycle is `Created -> Scheduled -> StartingRunners -> InProcessing -> PostProcessing -> Finished`, with `StartingRunners` repeating if a post-processing branch returns work to the runner startup path.
- Translator communication is responsible for the `Created -> Scheduled` handoff and failure recovery around that claim.
- `Scheduled` is the durable in-flight state for translation work. It means SM has claimed the scenario and may still need to resend the translation request until the Translator responds or the retry budget is exhausted.
- Retry handling is time-based. If no ready message arrives before the stale timeout, SM may return the scenario to `Created` and try again.

## State Transitions Covered By This Spec

| Current State | Event | Next State |
| --- | --- | --- |
| `Created` | SM claims a scenario for translation | `Scheduled` |
| `Scheduled` | Translator ready message accepted and container image persisted | `StartingRunners` |
| `Scheduled` | SM publish failure before request delivery | `Created` |
| `Scheduled` | stale timeout, attempts below max | `Created` |
| `Scheduled` | stale timeout, attempts reached max | `Failed` |

## Ownership And Claiming

There is no long-lived SQL row lock.

The durable claim is the state transition to `Scheduled`.

Claim flow:
1. Start a DB transaction.
2. Select one candidate row with `state = 'Created'`.
3. Use `FOR UPDATE SKIP LOCKED`.
4. Update the selected row in the same transaction:
   - `state = 'Scheduled'`
   - `translation_attempts = translation_attempts + 1`
   - `updated_at = NOW()`
5. Commit.
6. Publish the translation request after commit.

Reasoning:
- `FOR UPDATE SKIP LOCKED` prevents two SM instances from claiming the same row concurrently.
- The row lock exists only during the transaction.
- After commit, the state value is the durable claim marker.

If publish fails after commit:
1. log the failure
2. best-effort update the row back to `Created`
3. set `updated_at = NOW()`
4. this counts as a failed attempt for the current claim and will be retried only while `translation_attempts < MAX_ATTEMPTS`

## Selection Contract

`scenario-manager/internal/core/selector.go` owns SM-side scenario selection for any state transition that requires the Scenario Manager to take an action.

In v1, the only implemented selection path is the `Created -> Scheduled` translation claim. Later work may extend the same area to other SM-driven transitions, such as scheduling jobs when entering `StartingRunners`.

Required v1 behavior:
- select only rows in `Created`
- order by `priority DESC, id ASC`
- claim one scenario per selector call
- keep selection logic isolated in `scenario-manager/internal/core/selector.go`

Recommended initial loop behavior:
- poll interval: 5 seconds
- max newly claimed scenarios per loop per SM process: 1

This is intentionally conservative for v1. The selector layer is expected to grow additional responsibilities over time without changing the transport contract.

## NATS / JetStream Contract

Use the same shared NATS server and JetStream account already used by EDS.

### Subjects

Request subject template:
- `cbse.{project}.trans.request`

Ready subject template:
- `cbse.{project}.trans.{scenario_id}.ready`

Wildcard subject for SM ready consumption:
- `cbse.*.trans.*.ready`

`{project}` is `SimulationExperiment.metadata.name`, which is already persisted as the project name.

### Stream

Use one JetStream stream for translator communication:

| Setting | Value |
| --- | --- |
| stream name | `cbse_translator` |
| stream subjects | `cbse.*.trans.request`, `cbse.*.trans.*.ready` |

### SM Durable Consumer

SM needs only the ready-message consumer.

| Setting | Value |
| --- | --- |
| durable consumer | `scenario-manager-translator-ready` |
| queue group | `scenario-manager-translator-ready` |
| ack mode | manual ack |
| ack wait | 2 minutes |
| max ack pending | 1024 |

Ack rules:
- ACK only after a successful DB update or after classifying the message as a permanent poison message.
- NAK on transient DB or decoding failures.

### Environment Variables

Mirror the EDS configuration style. Add these env vars:

| Env Var | Default |
| --- | --- |
| `SCENARIO_MANAGER_TRANS_REQUEST_SUBJECT_TEMPLATE` | `cbse.{project}.trans.request` |
| `SCENARIO_MANAGER_TRANS_READY_SUBJECT_TEMPLATE` | `cbse.{project}.trans.{id}.ready` |
| `SCENARIO_MANAGER_TRANS_READY_WILDCARD_SUBJECT` | `cbse.*.trans.*.ready` |
| `SCENARIO_MANAGER_TRANS_STREAM` | `cbse_translator` |
| `SCENARIO_MANAGER_TRANS_READY_CONSUMER` | `scenario-manager-translator-ready` |
| `SCENARIO_MANAGER_TRANS_QUEUE_GROUP` | `scenario-manager-translator-ready` |
| `SCENARIO_MANAGER_TRANS_ACK_WAIT` | `2m` |
| `SCENARIO_MANAGER_TRANS_MAX_ACK_PENDING` | `1024` |
| `SCENARIO_MANAGER_TRANS_SELECT_INTERVAL` | `5s` |
| `SCENARIO_MANAGER_TRANS_STALE_TIMEOUT` | `10m` |
| `SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS` | `3` |

## Message Schemas

All payloads are JSON.

### Translation Request

Published by SM to `cbse.{project}.trans.request`.

```json
{
  "id": 42,
  "project": "demo-project",
  "project_id": 7,
  "recipe_info": {},
  "confidence_metric": 0.93
}
```

Field contract:

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `id` | integer | yes | scenario_status row id |
| `project` | string | yes | `SimulationExperiment.metadata.name` |
| `project_id` | integer | yes | DB project id |
| `recipe_info` | object or null | no | copied from `scenario_status.recipe_info` |
| `confidence_metric` | number or null | no | copied from `scenario_status.confidence_metric` |

### Ready Message

Published by Translator to `cbse.{project}.trans.{scenario_id}.ready`.

```json
{
  "id": 42,
  "project": "demo-project",
  "project_id": 7,
  "container_image": "registry.example.com/team/scenario:abc123"
}
```

Field contract:

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `id` | integer | yes | must match subject scenario id |
| `project` | string | yes | must match subject project token |
| `project_id` | integer | yes | must match DB row |
| `container_image` | string | yes | image to persist into `scenario_status.container_image` |

## Subject And Payload Validation

SM must validate all of the following before updating the DB:

1. subject matches the expected `ready` pattern
2. payload JSON decodes successfully
3. subject `{project}` equals payload `project`
4. subject `{scenario_id}` equals payload `id`
5. DB row exists
6. DB row `project_id` equals payload `project_id`

If validation fails because the message is malformed or references a nonexistent scenario:
- log the reason
- ACK the message

Reason: these are poison messages, not transient failures.

If validation fails due to transient DB or infrastructure errors:
- NAK the message

## Ready Handling And Idempotency

When SM consumes a valid ready message:
1. load the scenario row by `id`
2. verify the row is already `Scheduled`
3. if the row is `Scheduled`, update:
   - `container_image = <payload.container_image>`
   - `updated_at = NOW()`
4. if `container_image` is already present and matches the payload, treat as duplicate and ACK
5. if `container_image` is already present and differs, log an inconsistency and ACK
6. if state is in any other value, log and ACK

Reasoning:
- duplicate ready messages must not re-queue work
- redeliveries are expected in JetStream systems

## Stale Translation Recovery

Add a periodic recovery step in SM.

Every `SCENARIO_MANAGER_TRANS_SELECT_INTERVAL`, before selecting new work:
1. find rows where:
   - `state = 'Scheduled'`
   - `updated_at < NOW() - STALE_TIMEOUT`
2. for each stale row:
   - if `translation_attempts < MAX_ATTEMPTS`, set `state = 'Created'` and `updated_at = NOW()`
   - otherwise set `state = 'Failed'` and `updated_at = NOW()`

No heartbeat protocol is part of v1. Timeout-based recovery is the only recovery mechanism.
This recovery path covers both cases where the request was published but no ready message arrived, and cases where SM could not successfully complete the publish step. In both cases, `Scheduled` remains the retryable in-flight state.

## Startup Integration

`RunScenarioManager` must start translator communication in addition to EDS communication.

Startup order:
1. start the SimulationExperiment informer
2. start EDS communication
3. start Translator ready-message consumption
4. start the translation selection loop

Translator communication startup is a required dependency. Failure to initialize it must terminate the process.

## Required Functions

### `scenario-manager/internal/core/selector.go`

- `selectScenarioTrans(ctx context.Context) (*ScenarioForTranslation, error)`
- `recoverStaleScheduledScenarios(ctx context.Context) error`

`ScenarioForTranslation` should contain:
- `ID int`
- `Project string`
- `ProjectID int`
- `RecipeInfo json.RawMessage`
- `ConfidenceMetric *float64`

### `scenario-manager/internal/nats/trans_com.go`

- `StartTranslatorComms(ctx context.Context) error`
- `PublishTranslationRequest(ctx context.Context, scenario ScenarioForTranslation) error`
- `handleTranslatorReady(ctx context.Context, msg *nats.Msg, cfg transConfig)`
- `ensureTranslatorStream(js nats.JetStreamContext, cfg transConfig) error`
- `loadTranslatorConfig() transConfig`

### `scenario-manager/internal/coredb/scenario_status.go`

- `ClaimNextScenarioForTranslation(ctx context.Context) (*ScenarioForTranslation, error)`
- `MarkScenarioTranslationPublishFailed(ctx context.Context, scenarioID int) error`
- `MarkScenarioScheduled(ctx context.Context, scenarioID int, projectID int, containerImage string) error`
- `ResetStaleScheduledScenarios(ctx context.Context, staleBefore time.Time, maxAttempts int) (reset int, failed int, err error)`

## Logging Requirements

Each successful transition should log:
- scenario id
- project name
- old state
- new state

Each failure should log:
- scenario id if known
- subject if applicable
- failure class: decode, validation, DB, publish, or timeout recovery

## Non-Goals And Deferred Work

The following are explicitly deferred and should not block v1:
- heartbeat request/reply between SM and Translator
- dynamic resource-aware scheduling
- batching multiple translation requests into one message
- Translator error response messages beyond absence of ready

## Acceptance Criteria

The implementation is complete when all of the following are true:

1. EDS-created scenarios enter the DB as `Created`.
2. SM can claim exactly one `Created` scenario without duplicate claims across replicas.
3. SM publishes a valid request message to the project-specific translator request subject.
4. SM consumes a valid ready message and preserves the scenario in `Scheduled` while persisting the container image.
5. Duplicate ready messages do not corrupt state.
6. A publish failure returns the scenario to `Created`.
7. A stale `Scheduled` scenario is retried, then eventually marked `Failed` after the configured attempt limit.
