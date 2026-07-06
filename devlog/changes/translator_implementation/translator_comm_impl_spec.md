# Translator-Scenario Manager Communication

## Purpose

This document is the implementation-ready specification for the Scenario Manager (SM) to Translator communication workflow. The core workflow must be transport-neutral; NATS/JetStream is the v1 transport adapter.

Scope of this spec:
- executing the translator handoff for a scenario id supplied by higher-level SM orchestration
- publishing translation requests
- consuming Translator ready messages
- updating scenario state in the core database
- handling retries, duplicates, unpublished claims, and poison ready messages

Out of scope:
- deriving which scenario should start translation
- implementing the future translation selection loop in `scenario-manager/internal/core/scenario_manager.go`
- Translator internal processing logic
- runner startup after a scenario becomes `StartingRunners`
- heartbeat protocol between SM and Translator

## Target Files

Primary implementation locations:
- `scenario-manager/internal/nats/trans_com.go`
- `scenario-manager/internal/communication/communication.go`
- `scenario-manager/internal/subject/subject.go`
- `scenario-manager/internal/translatorconfig/translatorconfig.go`
- `scenario-manager/internal/core/translator_handoff.go`
- `scenario-manager/internal/coredb/scenario_status.go`
- `scenario-manager/internal/coredb/schema.go`

## Required Schema Changes

The current `scenario_status` table is not sufficient for robust claiming and unpublished-claim recovery. Add these columns:

| Column | Type | Purpose |
| --- | --- | --- |
| `created_at` | `TIMESTAMPTZ NOT NULL DEFAULT NOW()` | creation timestamp |
| `updated_at` | `TIMESTAMPTZ NOT NULL DEFAULT NOW()` | last state transition time |
| `translation_attempts` | `INTEGER NOT NULL DEFAULT 0` | retry counter |
| `translation_request_published_at` | `TIMESTAMPTZ` | set after SM confirms the translation request was accepted by the configured transport |

`updated_at` must be set on every scenario state transition handled by SM.

No database migration path is required for this implementation step. Update the schema creation path directly. Existing development databases will be manually adjusted or recreated; do not add additional migration helpers or `ALTER TABLE ... ADD COLUMN IF NOT EXISTS` compatibility code for this step.

## Canonical Scenario States

Use exactly these state strings:

1. `Created`
2. `Scheduled`
3. `StartingRunners`
4. `InProcessing`
5. `PostProcessing`
6. `Finished`
7. `Failed`

Notes:
- Replace the current default state `Pending` with `Created`.
- Do not use spaces in state names.
- The canonical lifecycle is `Created -> Scheduled -> StartingRunners -> InProcessing -> PostProcessing -> Finished`, with `StartingRunners` repeating if a post-processing branch returns work to the runner startup path.
- Translator communication is responsible for executing the `Created -> Scheduled` handoff for a specific scenario id supplied by higher-level SM orchestration.
- `Scheduled` means the translation request is claimed, pending, or being processed by the Translator. It is the durable in-flight state for translation work.
- `StartingRunners` means the Translator has replied, `container_image` is present, and SM-owned runner job scheduling may begin.
- Retry handling is not based on estimated translation duration. SM only recovers claims where the request was not confirmed as published; active published translations are not timed out in v1.

## State Transitions Covered By This Spec

| Current State | Event | Next State |
| --- | --- | --- |
| `Created` | SM claims a scenario for translation | `Scheduled` |
| `Scheduled` | Translator ready message accepted and container image persisted | `StartingRunners` |
| `Scheduled` | SM publish failure, attempts below max | `Created` |
| `Scheduled` | SM publish failure, attempts reached max | `Failed` |
| `Scheduled` | unpublished claim recovery, attempts below max | `Created` |
| `Scheduled` | unpublished claim recovery, attempts reached max | `Failed` |
| `Scheduled` | poison ready response, attempts below max | `Created` |
| `Scheduled` | poison ready response, attempts reached max | `Failed` |

## Ownership And Claiming

There is no long-lived SQL row lock.

The durable claim is the state transition to `Scheduled`.

Translator communication does not choose scenarios. A future higher-level Scenario Manager loop supplies the scenario id that should start translation, then calls `ProcessScenarioTrans(ctx, scenarioID, publisher)` with that exact row id and the configured translation request publisher.

Claim flow:
1. `ProcessScenarioTrans(ctx, scenarioID, publisher)` calls `ClaimScenarioForTranslation(ctx, scenarioID)`.
2. Start a DB transaction.
3. Lock only the supplied scenario row with `FOR UPDATE`.
4. If the row does not exist or its current state is not `Created`, return `(nil, nil)` from the claim helper. `ProcessScenarioTrans` must log that the handoff was skipped and return nil.
5. If the row is `Created`, update that exact row in the same transaction:
   - `state = 'Scheduled'`
   - `translation_attempts = translation_attempts + 1`
   - `translation_request_published_at = NULL`
   - `updated_at = NOW()`
6. Return the claimed `ScenarioForTranslation`, including the incremented attempt number.
7. Commit.
8. Publish the translation request after commit.
9. A publish is successful only after the configured transport confirms publish acceptance.
10. After successful publish confirmation, call `MarkScenarioTranslationRequestPublished(ctx, scenarioID, attempt)` to set `translation_request_published_at = NOW()` for the exact claimed attempt.

Reasoning:
- The future main SM loop owns the orchestration that supplies the scenario id.
- `ClaimScenarioForTranslation` must never search for or fall back to a different row.
- `FOR UPDATE` serializes concurrent attempts to claim the same scenario id.
- The row lock exists only during the transaction.
- After commit, the state value is the durable claim marker.

If publish fails after commit:
1. log the failure
2. call `MarkScenarioTranslationPublishFailed(ctx, scenarioID, attempt, maxAttempts)`, which uses the same target-state calculation as `MarkScenarioTranslationAttemptFailed`
3. update only if the row still matches the exact claimed attempt:
   - `id = scenarioID`
   - `state = 'Scheduled'`
   - `translation_attempts = attempt`
   - `translation_request_published_at IS NULL`
4. if `attempt < maxAttempts`, update the row back to `Created` and keep `translation_request_published_at = NULL`
5. if `attempt >= maxAttempts`, update the row to `Failed`
6. set `updated_at = NOW()` when the conditional update succeeds
7. if no row is updated, log that the publish failure was stale or already handled and do not overwrite the newer state

`MarkScenarioTranslationAttemptFailed` is the generic recovery helper for an exact `Scheduled` attempt and does not require `translation_request_published_at IS NULL`. `MarkScenarioTranslationPublishFailed` is stricter because it represents a failed publish before SM recorded the request as published.

The attempt counter is incremented when the scenario is claimed. A publish failure consumes the already-counted attempt and must not increment `translation_attempts` again.

If SM receives successful transport publish confirmation but `MarkScenarioTranslationRequestPublished` returns a DB error, do not call publish-failure recovery because the request may already be durably accepted by the transport. Log and return the DB error; unpublished-claim recovery may retry the scenario later. The `translation_attempt` field keeps late ready messages from older attempts from being accepted into the wrong DB state.

If `MarkScenarioTranslationRequestPublished` returns `false, nil` after confirmed transport publish, treat it as a stale publish-marker update: log it and return nil. Do not call publish-failure recovery, because the request was accepted by the transport and the row may already have advanced past `Scheduled`.

If SM crashes after transport publish confirmation but before `translation_request_published_at` is persisted, unpublished-claim recovery may retry the scenario later. The `translation_attempt` field keeps late ready messages from older attempts from being accepted into the wrong DB state.

## Translation Handoff Contract

`ProcessScenarioTrans(ctx, scenarioID, publisher)` owns the translator handoff for a scenario id already supplied by higher-level SM orchestration.

Required v1 behavior:
- do not choose a scenario
- do not scan for other `Created` rows
- claim only the supplied `scenarioID`
- publish only after a successful claim commit
- publish through a `TranslationRequestPublisher` implementation
- mark the exact claimed attempt as published only after the publisher returns successful transport confirmation
- recover the exact claimed attempt on publish failure
- return nil when the supplied row is no longer claimable

Recommended placement:
- keep `ProcessScenarioTrans` in `scenario-manager/internal/core/translator_handoff.go`
- the future orchestration loop may call it from `scenario-manager/internal/core/scenario_manager.go`

This preserves a clean boundary: future SM orchestration supplies the scenario id; translator handoff executes the state transition and delegates transport delivery through a small broker-neutral communication interface.

## Translator Communication Lifecycle

Translator communication adapter lifecycle is process-scoped, not scenario-scoped.

A future revision of `scenario-manager/internal/core/scenario_manager.go` startup logic will discover or select the configured communication adapter, initialize it once for the Scenario Manager process, and start the Translator ready-message consumer once. `scenario-manager/cmd/main.go` is not responsible for translator adapter orchestration.

The initialized adapter must provide a reusable `communication.TranslationRequestPublisher`. Higher-level Scenario Manager orchestration reuses that publisher when calling `ProcessScenarioTrans(ctx, scenarioID, publisher)` for individual scenarios.

`ProcessScenarioTrans` is scenario-scoped and may be called many times during the lifetime of one Scenario Manager process. Each call handles exactly one supplied scenario id: it attempts to claim that row, publishes one translation request only if the claim succeeds, records publish confirmation, and returns.

`ProcessScenarioTrans` must not initialize the communication adapter, create a NATS connection, create a JetStream context, ensure streams, or start ready-message consumption. Those are process-scoped adapter lifecycle responsibilities owned by Scenario Manager startup logic.

Adapter discovery/selection and the future scenario-selection loop are out of scope for this implementation step. This spec only requires the callable adapter initialization surface and the repeatable per-scenario handoff function.

## Communication Abstractions

The reusable communication interfaces must live in `scenario-manager/internal/communication/communication.go` in package `communication`.

This package is the boundary between core orchestration and concrete transport adapters. It must stay lightweight, component-oriented, and free of NATS, JetStream, Kafka, or other broker-specific types. It exists outside `internal/core` so the current EDS code can remain unchanged while avoiding a Go import cycle between `internal/core` and `internal/nats`.

General pattern:
- adapter startup methods accept `context.Context`
- publishers return nil only after the transport confirms publish acceptance
- consumers invoke core-owned semantic handler functions and map handler outcomes to transport-specific ACK, retry, commit, or poison-message behavior
- domain payloads stay close to their owning workflow unless multiple transports or components need to share them
- reusable interfaces must not be defined in `internal/nats`
- `internal/communication` must not import `internal/core` or `internal/nats`
- `internal/communication` may import `internal/coredb` for the v1 translation request publisher interface, because `coredb.ScenarioForTranslation` is the claimed DB projection owned by the handoff workflow and this dependency does not create an import cycle

Ownership split:
- `internal/nats` owns NATS connection checks, JetStream setup, subject parsing, strict raw JSON decoding, transport-level validation, publish confirmation, and ACK/NAK mapping.
- `internal/communication` owns transport-neutral interface, message, and handler-result types.
- `internal/core` owns semantic ready handling, including empty-image recovery decisions.
- `internal/coredb` owns SQL state transitions and classified DB outcomes without transport concepts.

Translator-specific interfaces:

```go
type TranslationRequestPublisher interface {
	PublishTranslationRequest(ctx context.Context, scenario coredb.ScenarioForTranslation) error
}

type TranslatorReadyConsumer interface {
	StartTranslatorReadyConsumer(ctx context.Context, handler TranslatorReadyHandler) error
}

type TranslatorReadyHandler func(ctx context.Context, ready TranslatorReadyMessage) TranslatorReadyHandlingResult
```

`TranslatorReadyMessage` and `TranslatorReadyHandlingResult` are communication-owned transport-neutral types. They must not expose NATS messages, JetStream metadata, Kafka records, offsets, or partition details.

```go
type TranslatorReadyMessage struct {
	Project            string
	ScenarioID         int
	TranslationAttempt int
	ContainerImage     string
}

type TranslatorReadyHandlingStatus string

const (
	TranslatorReadyHandled TranslatorReadyHandlingStatus = "Handled"
	TranslatorReadyRetry   TranslatorReadyHandlingStatus = "Retry"
)

type TranslatorReadyHandlingResult struct {
	Status TranslatorReadyHandlingStatus
	Reason string
}
```

`TranslatorReadyHandled` maps to JetStream ACK. `TranslatorReadyRetry` maps to JetStream NAK.

For v1, `ProcessScenarioTrans(ctx, scenarioID, publisher)` depends on `TranslationRequestPublisher`. The NATS implementation satisfies that interface by publishing to JetStream and waiting for `PubAck`.

Reuse guidance for other components:
- EDS can later move from `nats.EDSBatchProcessor` to a broker-neutral batch handler type in `communication.go`
- PostProcessingService and future components should add narrow publisher or consumer interfaces only when core orchestration needs them
- component-specific state machines and DB transitions stay in core/coredb; adapters only translate between broker messages and core handlers
- each adapter owns its transport configuration, connection checks, publish confirmation, delivery acknowledgement, retry, and poison-message mapping

### Future Kafka Compatibility

Kafka support is not part of this implementation. It should be possible later by adding a Kafka adapter that implements the same core communication interfaces.

Expected mapping:
- NATS subjects map to Kafka topics and message keys
- JetStream `PubAck` maps to Kafka producer delivery confirmation
- JetStream ACK maps to Kafka offset commit
- JetStream NAK or redelivery maps to a retry topic, delayed retry, or DLQ policy

Kafka support must not change core DB state transitions, translator message schemas, stale-attempt handling, duplicate handling, or poison-message classification.

## NATS / JetStream Contract

NATS/JetStream is the v1 transport adapter for the core communication interfaces. Use the same shared NATS server and JetStream account already used by EDS.

### Subjects

Request subject template:
- `cbse.{project}.trans.request`

Ready subject template:
- `cbse.{project}.trans.{scenario_id}.ready`

Wildcard subject for SM ready consumption:
- `cbse.*.trans.*.ready`

`{project}` is the raw project name stored in the core DB project table, normalized with the shared subject-token normalization helper. The numeric `project_id` is internal to Scenario Manager and must not be required from Translator-facing subjects or payloads.

`project.project_name` remains the raw Kubernetes `SimulationExperiment.metadata.name`. Do not normalize project names when inserting, updating, deleting, or reading project rows. Normalize only when building or validating broker subjects.

### Stream

Use one JetStream stream for translator communication:

| Setting | Value |
| --- | --- |
| stream name | `cbse_translator` |
| stream subjects | `cbse.*.trans.request`, `cbse.*.trans.*.ready` |
| retention | `natsgo.WorkQueuePolicy` |
| storage | `natsgo.FileStorage` |
| discard policy | `natsgo.DiscardOld` |

The stream subject values above are defaults. `loadTranslatorConfig()` must derive the configured request stream subject from the request subject template by replacing the project placeholder with `*`. The configured ready stream subject should be the configured ready wildcard subject. `ensureTranslatorStream` must use these configured stream subjects from `transConfig`, not hardcoded default subjects.

`ensureTranslatorStream(js, cfg)` must mirror the EDS stream helper:
- look up the configured stream name
- create it when it does not exist
- fail on lookup errors other than `natsgo.ErrStreamNotFound`
- if the stream exists, append any missing translator subjects and call `UpdateStream`
- do not remove existing stream subjects

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
- ACK malformed JSON, unknown JSON fields, validation failures, stale attempts, missing rows, and other permanent poison messages.
- NAK only on transient DB or infrastructure failures.

These ACK/NAK rules are NATS adapter behavior. Core ready handlers return transport-neutral outcomes; the NATS adapter maps those outcomes to JetStream ACK, NAK, or poison-message ACK.

`NewTranslatorComms(ctx)` must mirror the EDS startup shape for one process-scoped adapter:
1. read config with `loadTranslatorConfig`
2. verify the shared NATS connection exists and is connected
3. create a JetStream context
4. call `ensureTranslatorStream`
5. return a `*TranslatorComms` containing the connection, JetStream context, and config

`(*TranslatorComms).StartTranslatorReadyConsumer(ctx, handler)` must:
1. subscribe to the ready wildcard with `QueueSubscribe`, the configured queue group, durable name, manual ACK, ACK wait, and max ACK pending
2. flush the NATS connection
3. log the ready subject, stream, durable consumer, and queue group
4. on context cancellation, do not explicitly unsubscribe the JetStream durable subscription, matching the EDS rolling-restart behavior

The v1 Scenario Manager ready consumer is process-scoped and uses the configured wildcard subject, defaulting to `cbse.*.trans.*.ready`. Do not create per-scenario ready subscriptions after individual request publishes in this implementation.

`(*TranslatorComms).PublishTranslationRequest` must satisfy `TranslationRequestPublisher`. It must publish to JetStream and return nil only after a successful `PubAck`, which is the v1 implementation of transport publish confirmation.

`(*TranslatorComms).PublishTranslationRequest` builds the request subject from `ScenarioForTranslation.Project`. It must normalize the project name with the same EDS-style subject-token normalization used for validation, replace `{project}` or `%s` in the configured request template, and reject templates where the project placeholder is missing or unresolved.

### Environment Variables

Mirror the EDS configuration style. Add these env vars:

| Env Var | Default |
| --- | --- |
| `SCENARIO_MANAGER_TRANS_REQUEST_SUBJECT_TEMPLATE` | `cbse.{project}.trans.request` |
| `SCENARIO_MANAGER_TRANS_READY_SUBJECT_TEMPLATE` | `cbse.{project}.trans.{scenario_id}.ready` |
| `SCENARIO_MANAGER_TRANS_READY_WILDCARD_SUBJECT` | `cbse.*.trans.*.ready` |
| `SCENARIO_MANAGER_TRANS_STREAM` | `cbse_translator` |
| `SCENARIO_MANAGER_TRANS_READY_CONSUMER` | `scenario-manager-translator-ready` |
| `SCENARIO_MANAGER_TRANS_QUEUE_GROUP` | `scenario-manager-translator-ready` |
| `SCENARIO_MANAGER_TRANS_ACK_WAIT` | `2m` |
| `SCENARIO_MANAGER_TRANS_MAX_ACK_PENDING` | `1024` |
| `SCENARIO_MANAGER_TRANS_PUBLISH_RECOVERY_TIMEOUT` | `1m` |
| `SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS` | `3` |

Parsing rules:
- string values use the existing `envOrDefault` pattern
- durations use `time.ParseDuration`; invalid or non-positive values log and fall back to defaults
- integers use `strconv.Atoi`; invalid or non-positive values log and fall back to defaults
- request subject templates must contain `{project}` or `%s`
- ready subject templates must contain a project placeholder and `{scenario_id}`
- project subject tokens are normalized with `subject.NormalizeToken`
- wildcard subjects may be configured directly and are not derived from templates at runtime

`SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS` is parsed by `translatorconfig.LoadMaxAttempts()`, which requires a positive integer, logs invalid values, and falls back to `3`. Both `loadTranslatorConfig()` and `loadTranslatorHandoffConfig()` must call this shared helper so ready handling, handoff publishing, and unpublished-claim recovery use the same value.

`SCENARIO_MANAGER_TRANS_PUBLISH_RECOVERY_TIMEOUT` is parsed by `translatorconfig.LoadPublishRecoveryTimeout()`, which requires a positive duration, logs invalid values, and falls back to `1m`.

`scenario-manager/internal/translatorconfig` is translator-workflow specific but package-cycle neutral. It must not import `internal/core`, `internal/nats`, `internal/communication`, broker clients, or database packages. `internal/core` and `internal/nats` may both import `internal/translatorconfig`; `internal/nats` must not import `internal/core`.

`SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS` is included in `transConfig` so `handleTranslatorReady` can apply failed-reschedule policy for empty-image ready messages.

## Message Schemas

All payloads are JSON.
No extra JSON fields are allowed. Decoding must use `json.Decoder.DisallowUnknownFields` for consumed payloads and must reject trailing JSON tokens after the first decoded object.

### Translation Request

Published by SM to `cbse.{project}.trans.request`.

```json
{
  "id": 42,
  "translation_attempt": 1,
  "recipe_info": {},
  "confidence_metric": 0.93
}
```

Field contract:

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `id` | integer | yes | scenario_status row id |
| `translation_attempt` | integer | yes | incremented `scenario_status.translation_attempts` value returned by the successful claim |
| `recipe_info` | object or null | no | copied from `scenario_status.recipe_info` |
| `confidence_metric` | number or null | no | copied from `scenario_status.confidence_metric` |

Translator must treat request payload `id` as the scenario id and use it as `{scenario_id}` when publishing the ready message subject.

### Ready Message

Published by Translator to `cbse.{project}.trans.{scenario_id}.ready`.

The ready subject is the authoritative transport-level identity. Scenario Manager must parse `{project}` and `{scenario_id}` from the subject before invoking core semantic handling. Subject `{scenario_id}` is identical to `scenario_status.id`; it is the same value sent as request payload `id`.

```json
{
  "translation_attempt": 1,
  "container_image": "registry.example.com/team/scenario:abc123"
}
```

Field contract:

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `translation_attempt` | integer | yes | must match current DB `translation_attempts` value |
| `container_image` | string | yes | image to persist into `scenario_status.container_image` |

## Subject And Payload Validation

The NATS adapter must validate all of the following before invoking core semantic ready handling:

1. subject matches the configured ready subject template shape, defaulting to `cbse.{project}.trans.{scenario_id}.ready`
2. subject `{project}` is not empty
3. subject `{scenario_id}` is a positive integer
4. payload JSON decodes successfully with unknown fields rejected
5. payload `translation_attempt` is a positive integer
6. string fields are trimmed before validation and before building `communication.TranslatorReadyMessage`

Ready subject template matching is segment-by-segment after splitting on `.`. In v1, the only dynamic ready-subject segments are `{project}` and `{scenario_id}`; all other template segments are literals and must match exactly. The configured ready subject template must contain both dynamic segments exactly once. The NATS adapter gets `{scenario_id}` from the ready subject and validates it against `scenario_status.id`; `{project}` is transport routing context and is not resolved to `project.id` during ready-message handling.

The request subject template has one dynamic segment, `{project}` or `%s`, supplied from `ScenarioForTranslation.Project`, which is loaded from the project table during the claim query and normalized only for the broker subject. The request subject does not contain `{scenario_id}`; the scenario id is carried as request payload field `id`.

After transport validation, the NATS adapter builds `communication.TranslatorReadyMessage` from subject fields plus payload fields:
- `Project` from subject `{project}`
- `ScenarioID` from subject `{scenario_id}`
- `TranslationAttempt` from payload `translation_attempt`
- `ContainerImage` from payload `container_image`

Core/coredb must then validate:
1. scenario row exists where `scenario_status.id = ScenarioID`
2. scenario row `translation_attempts` equals `TranslationAttempt`

The project-token normalization used for publishing and transport subject validation must match the existing EDS-style `sanitizeSubjectToken` behavior. Implement the pure helper as `subject.NormalizeToken` in `scenario-manager/internal/subject`; `internal/nats` and future communication adapters must use this helper rather than duplicating normalization logic. The existing private EDS `sanitizeSubjectToken` helper may remain for continuity, but it must delegate to `subject.NormalizeToken`.

Ready-message DB handling does not resolve subject `{project}` to `project.id`. `scenario_status.id` is the unique scenario identity and already localizes the row to its owning project through `scenario_status.project_id`. The ready subject project token is validated as transport routing context only.

Do not validate container image syntax in v1. Container image references may come from different registries and formats, and stricter syntax checks are intentionally deferred.

An empty `container_image` is not rejected by the NATS adapter as malformed transport payload. After subject shape, strict JSON shape, positive scenario id, positive `translation_attempt`, and non-empty subject project are validated, the NATS adapter must pass the trimmed empty image to core semantic handling so core can recover or fail the exact scheduled attempt.

If validation fails because the subject is malformed, subject scenario id is non-positive, the message payload is malformed, contains unknown fields, references a nonexistent scenario, or references an old `translation_attempt`:
- log the reason
- ACK the message

Reason: these are poison messages, not transient failures.

If `container_image` is empty, classify the ready message as a poison Translator response for the current attempt after SM has validated enough information to identify the exact row:
1. validate subject shape, strict JSON shape, positive subject scenario id, positive `translation_attempt`, and non-empty subject project
2. load the scenario row where `scenario_status.id = scenario_id`
3. validate the row attempt
4. call `MarkScenarioTranslationAttemptFailed(ctx, scenarioID, attempt, maxAttempts)`
5. if attempts remain, transition the exact `Scheduled` attempt back to `Created`
6. if attempts are exhausted, transition the exact `Scheduled` attempt to `Failed`
7. do not increment `translation_attempts` during this recovery update; the current attempt was already counted when the scenario was claimed
8. if the scenario returns to `Created`, the next claim increments `translation_attempts` for the next attempt
9. ACK the message after DB recovery succeeds
10. NAK only if that DB recovery update fails transiently

If validation fails due to transient DB or infrastructure errors:
- NAK the message

## Ready Handling And Idempotency

When SM consumes a ready message:
1. load the scenario row where `scenario_status.id = subject scenario_id`
2. classify missing rows as poison and ACK
3. verify the row `translation_attempts` equals payload `translation_attempt`
4. if the attempt does not match, classify as stale and ACK
5. if the row is `Scheduled` and the attempt matches with a non-empty image, update:
   - `state = 'StartingRunners'`
   - `container_image = <payload.container_image>`
   - `updated_at = NOW()`
6. if the row is already `StartingRunners`, `translation_attempts` matches, and `container_image` matches the payload, treat as duplicate and ACK
7. if the row is already `StartingRunners` and `container_image` differs, log an inconsistency and ACK
8. if the row is `Created`, `Failed`, or any other value, classify as ignored/stale and ACK without changing DB state

Reasoning:
- duplicate ready messages must not re-queue work
- redeliveries are expected in JetStream systems
- a ready message should not arrive while the row is `Created`; if it does, SM treats it as stale or invalid for the current state
- a late ready message from an older claim attempt is rejected because `translation_attempt` does not match the current DB value
- an empty-image ready message is a poison Translator response for the current attempt; SM recovers or fails that exact attempt immediately so the row does not remain stuck in `Scheduled`

## Unpublished Claim Recovery

Add a callable unpublished-claim recovery helper for the future main SM loop. Recovery is exact-row and uses the existing primary-key index on `scenario_status.id`; do not add a new recovery index for v1.

When invoked by higher-level SM orchestration for a known `scenarioID`:
1. update only the row where:
   - `id = scenarioID`
   - `state = 'Scheduled'`
   - `translation_request_published_at IS NULL`
   - `updated_at < NOW() - PUBLISH_RECOVERY_TIMEOUT`
2. if the exact row matches and `translation_attempts < MAX_ATTEMPTS`, set `state = 'Created'` and `updated_at = NOW()`
3. if the exact row matches and `translation_attempts >= MAX_ATTEMPTS`, set `state = 'Failed'` and `updated_at = NOW()`
4. if no row matches, return `stateChanged = false` and do not treat it as an error

Do not recover or fail `Scheduled` rows where `translation_request_published_at IS NOT NULL`. Once the request is marked published, SM treats the Translator as actively responsible for the work until a ready message, heartbeat protocol, explicit Translator failure message, or cancellation protocol exists.

No heartbeat protocol is part of v1. Timeout-based recovery applies only to the short SM-owned window between claiming a scenario and recording successful transport publish confirmation.
This recovery path mainly covers cases where SM stopped after claiming the scenario but before completing publish success or publish-failure handling. Immediate publish errors are handled by the publish-failure path above.

This spec does not implement or schedule the future orchestration loop that decides which `scenarioID` should be passed to unpublished-claim recovery.

## Future Startup Integration

The future `scenario-manager/internal/core/scenario_manager.go` startup logic will discover or select the configured communication adapters and initialize the selected adapter. That discovery and selection logic is intentionally out of scope for this implementation step.

This implementation must expose a callable translator communication initialization surface that `scenario_manager.go` can use later. The callable surface initializes the process-scoped translator communication adapter and returns a value that satisfies `communication.TranslationRequestPublisher`; the same adapter value also starts ready-message consumption through `communication.TranslatorReadyConsumer`.

Do not modify `RunScenarioManager` or wire translator adapter startup into `scenario-manager/internal/core/scenario_manager.go` as part of this implementation step. Future startup orchestration will decide when to initialize the adapter, start ready consumption, and call `ProcessScenarioTrans`.

`scenario-manager/cmd/main.go` is not responsible for translator adapter orchestration.

NATS/JetStream is the default and only required v1 adapter.

## Required Functions

### `scenario-manager/internal/communication/communication.go`

This file must define the reusable communication boundary for core-owned orchestration and concrete adapters. It must be in package `communication`.

Required translator interfaces:
- `TranslationRequestPublisher`
- `TranslatorReadyConsumer`
- `TranslatorReadyHandler`

Required transport-neutral translator message/result types:
- `TranslatorReadyMessage`
- `TranslatorReadyHandlingResult`

The types in this file must not depend on NATS, JetStream, Kafka, `internal/core`, or any broker-specific client type. The package may import `internal/coredb` only for `coredb.ScenarioForTranslation` in `TranslationRequestPublisher`; do not add additional coredb dependencies unless the workflow explicitly needs them.

EDS and future component communication should reuse this pattern. Do not refactor existing EDS code as part of this translator implementation, but future EDS modularization should move the current `nats.EDSBatchProcessor` style callback into a broker-neutral handler type.

### `scenario-manager/internal/subject/subject.go`

- `NormalizeToken(value string) string`

`NormalizeToken` must implement the current EDS `sanitizeSubjectToken` behavior:
- lowercase the input
- keep ASCII letters, digits, `-`, and `_`
- replace every other rune with `-`
- return an empty string for empty input

Project names in the database remain raw Kubernetes `SimulationExperiment.metadata.name` values. The shared normalizer is only for broker subject tokens. The existing EDS `sanitizeSubjectToken` helper must delegate to `subject.NormalizeToken` instead of duplicating the algorithm.

### `scenario-manager/internal/translatorconfig/translatorconfig.go`

This package contains shared translator-workflow configuration parsing used by multiple packages without creating an import cycle. It is not a generic component configuration package.

Required exported constants:
- `MaxAttemptsEnv = "SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS"`
- `PublishRecoveryTimeoutEnv = "SCENARIO_MANAGER_TRANS_PUBLISH_RECOVERY_TIMEOUT"`
- `DefaultMaxAttempts = 3`
- `DefaultPublishRecoveryTimeout = time.Minute`

Required functions:
- `LoadMaxAttempts() int`
- `LoadPublishRecoveryTimeout() time.Duration`

Parsing behavior:
- `LoadMaxAttempts` uses `strconv.Atoi`, requires a positive integer, logs invalid values, and falls back to `DefaultMaxAttempts`
- `LoadPublishRecoveryTimeout` uses `time.ParseDuration`, requires a positive duration, logs invalid values, and falls back to `DefaultPublishRecoveryTimeout`

Import rules:
- `internal/translatorconfig` must not import `internal/core`, `internal/nats`, `internal/communication`, `internal/coredb`, broker client packages, or database packages
- `internal/core` may import `internal/translatorconfig`
- `internal/nats` may import `internal/translatorconfig`
- `internal/nats` must not import `internal/core`

### `scenario-manager/internal/core/translator_handoff.go`

- `ProcessScenarioTrans(ctx context.Context, scenarioID int, publisher communication.TranslationRequestPublisher) error`
- `RecoverUnpublishedTranslationClaim(ctx context.Context, scenarioID int) (stateChanged bool, finalState string, err error)`
- `HandleTranslatorReady(ctx context.Context, ready communication.TranslatorReadyMessage) communication.TranslatorReadyHandlingResult`
- `loadTranslatorHandoffConfig() translatorHandoffConfig`

`translatorHandoffConfig` should contain:
- `MaxAttempts int`
- `PublishRecoveryTimeout time.Duration`

`loadTranslatorHandoffConfig()` must call `translatorconfig.LoadMaxAttempts()` and `translatorconfig.LoadPublishRecoveryTimeout()`. Do not place shared translator env parsing in `internal/core`, because `internal/nats` also needs the same values and must not import `internal/core`.

`ProcessScenarioTrans` must call `publisher.PublishTranslationRequest(ctx, *claimedScenario)` after the DB claim commits. If the publisher returns an error, execute the existing publish-failure recovery path. If the publisher returns nil, mark the exact claimed attempt as published. If that publish-marker update returns `false, nil`, log a stale publish-marker update and return nil without calling publish-failure recovery.

`HandleTranslatorReady` is the semantic ready-message handler used by transport adapters after transport-level validation. It must trim and validate `ContainerImage`, call `coredb.MarkScenarioTranslationAttemptFailed` for empty-image ready messages, call `coredb.MarkScenarioTranslatorReady` for non-empty images, map terminal DB outcomes to `communication.TranslatorReadyHandled`, and map transient DB errors to `communication.TranslatorReadyRetry`.

### `scenario-manager/internal/coredb/scenario_status.go`

- `ClaimScenarioForTranslation(ctx context.Context, scenarioID int) (*ScenarioForTranslation, error)`
- `MarkScenarioTranslationRequestPublished(ctx context.Context, scenarioID int, attempt int) (bool, error)`
- `MarkScenarioTranslationAttemptFailed(ctx context.Context, scenarioID int, attempt int, maxAttempts int) (stateChanged bool, finalState string, err error)`
- `MarkScenarioTranslationPublishFailed(ctx context.Context, scenarioID int, attempt int, maxAttempts int) (stateChanged bool, finalState string, err error)`
- `MarkScenarioTranslatorReady(ctx context.Context, scenarioID int, translationAttempt int, containerImage string) (TranslatorReadyResult, error)`
- `RecoverUnpublishedTranslationClaim(ctx context.Context, scenarioID int, claimedBefore time.Time, maxAttempts int) (stateChanged bool, finalState string, err error)`

`ScenarioForTranslation` should be defined in `coredb` and contain:
- `ID int`
- `Project string`
- `TranslationAttempt int`
- `RecipeInfo json.RawMessage`
- `ConfidenceMetric *float64`

`TranslatorReadyResult` should classify expected DB outcomes without using errors:

```go
type TranslatorReadyStatus string

const (
	TranslatorReadyApplied      TranslatorReadyStatus = "Applied"
	TranslatorReadyDuplicate    TranslatorReadyStatus = "Duplicate"
	TranslatorReadyConflict     TranslatorReadyStatus = "Conflict"
	TranslatorReadyIgnoredStale TranslatorReadyStatus = "IgnoredStale"
	TranslatorReadyPoison       TranslatorReadyStatus = "Poison"
)

type TranslatorReadyResult struct {
	Status        TranslatorReadyStatus
	PreviousState string
	CurrentState  string
}
```

Status meanings:
- `TranslatorReadyApplied`: row was `Scheduled`, attempt matched, and state moved to `StartingRunners`
- `TranslatorReadyDuplicate`: row was already `StartingRunners` with the same attempt and image
- `TranslatorReadyConflict`: row was already `StartingRunners` with the same attempt but a different image
- `TranslatorReadyIgnoredStale`: attempt mismatch or a non-`Scheduled` state that should not be changed
- `TranslatorReadyPoison`: row missing for the supplied scenario id

Errors from `MarkScenarioTranslatorReady` are reserved for transient DB or infrastructure failures.

Ready handling must classify `TranslatorReadyApplied`, `TranslatorReadyDuplicate`, `TranslatorReadyConflict`, `TranslatorReadyIgnoredStale`, and `TranslatorReadyPoison` as successful terminal handling outcomes. The NATS adapter maps these outcomes to ACK. It maps transient errors to NAK.

Coredb transition semantics:
- public helpers reject non-positive IDs or attempts with an error before executing SQL
- `ClaimScenarioForTranslation` returns `(nil, nil)` when the row is missing or not `Created`
- `MarkScenarioTranslationRequestPublished` updates only `id = scenarioID`, `state = 'Scheduled'`, `translation_attempts = attempt`, and `translation_request_published_at IS NULL`
- `MarkScenarioTranslationAttemptFailed` updates only `id = scenarioID`, `state = 'Scheduled'`, and `translation_attempts = attempt`
- `MarkScenarioTranslationPublishFailed` additionally requires `translation_request_published_at IS NULL`
- `MarkScenarioTranslatorReady` targets only the scenario row where `scenario_status.id = scenarioID`
- `MarkScenarioTranslatorReady` must classify a missing scenario row as `TranslatorReadyPoison` without returning an error
- recovery helpers return counts and do not treat zero affected rows as errors

### `scenario-manager/internal/nats/trans_com.go`

Define the concrete process-scoped adapter:

```go
type TranslatorComms struct {
	nc  *natsgo.Conn
	js  natsgo.JetStreamContext
	cfg transConfig
}
```

Required constructor and methods:

- `NewTranslatorComms(ctx context.Context) (*TranslatorComms, error)`
- `func (c *TranslatorComms) StartTranslatorReadyConsumer(ctx context.Context, handler communication.TranslatorReadyHandler) error`
- `func (c *TranslatorComms) PublishTranslationRequest(ctx context.Context, scenario coredb.ScenarioForTranslation) error`
- `handleTranslatorReady(ctx context.Context, msg *natsgo.Msg, cfg transConfig, handler communication.TranslatorReadyHandler)`
- `ensureTranslatorStream(js natsgo.JetStreamContext, cfg transConfig) error`
- `loadTranslatorConfig() transConfig`

The NATS translator communication implementation must satisfy the `communication.TranslationRequestPublisher` and `communication.TranslatorReadyConsumer` interfaces through methods on `*TranslatorComms`.

An optional package helper may wrap construction and consumer startup:

- `StartTranslatorComms(ctx context.Context, handler communication.TranslatorReadyHandler) (*TranslatorComms, error)`

This helper must not be the only interface-shaped API; interface satisfaction must come from methods on `*TranslatorComms`.

`(*TranslatorComms).PublishTranslationRequest` must use JetStream publish semantics, wait for the server `PubAck`, and return nil only after a successful `PubAck`. If JetStream does not return a successful `PubAck`, return an error and do not mark `translation_request_published_at`.

`transConfig` should include:
- request subject template
- request stream subject
- ready subject template
- ready wildcard subject
- stream name
- ready durable consumer
- queue group
- ACK wait
- max ACK pending
- max attempts

## Logging Requirements

Each successful transition should log:
- scenario id
- project name
- old state
- new state

Each failure should log:
- scenario id if known
- subject if applicable
- failure class: decode, validation, DB, publish, poison ready, or unpublished-claim recovery

## Non-Goals And Deferred Work

The following are explicitly deferred and should not block v1:
- deriving which scenario id to pass into `ProcessScenarioTrans`
- adapter discovery and selection in `scenario-manager/internal/core/scenario_manager.go`
- heartbeat request/reply between SM and Translator
- dynamic resource-aware scheduling
- batching multiple translation requests into one message
- Translator error response messages beyond absence of ready
- automated tests for this implementation step; validation is by spec review, code review, and later manual deployment checks

## Completion Criteria

The implementation is complete when SM can execute the translator handoff for a supplied scenario id through a `TranslationRequestPublisher`, persist successful transport publish confirmation, consume ready messages idempotently through the configured adapter, apply failed-reschedule policy for publish failures and empty-image ready messages, recover a supplied unpublished claim by scenario id, and leave published active translations untouched until Translator replies or a future protocol is added.
