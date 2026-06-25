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
- `scenario-manager/internal/core/communication.go`
- `scenario-manager/internal/core/translator_handoff.go`
- `scenario-manager/internal/core/scenario_manager.go`
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

No database migration path is required for this implementation step. The project is under active development and current databases are disposable development/test databases, so update the schema creation path directly.

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

If SM receives successful transport publish confirmation but `MarkScenarioTranslationRequestPublished` fails, do not call publish-failure recovery because the request may already be durably accepted by the transport. Log and return the DB error; unpublished-claim recovery may retry the scenario later. The `translation_attempt` field keeps late ready messages from older attempts from being accepted into the wrong DB state.

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

This preserves a clean boundary: future SM orchestration supplies the scenario id; translator handoff executes the state transition and delegates transport delivery through a small core-owned communication interface.

## Core Communication Abstractions

The reusable communication interfaces must live in `scenario-manager/internal/core/communication.go` in package `core`.

This file is the boundary between core orchestration and concrete transport adapters. It must stay lightweight, component-oriented, and free of NATS, JetStream, Kafka, or other broker-specific types.

General pattern:
- adapter startup methods accept `context.Context`
- publishers return nil only after the transport confirms publish acceptance
- consumers invoke core-owned handler functions and map handler outcomes to transport-specific ACK, retry, commit, or poison-message behavior
- domain payloads stay close to their owning workflow unless multiple transports or components need to share them
- reusable interfaces must not be defined in `internal/nats`

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

`TranslatorReadyMessage` and `TranslatorReadyHandlingResult` should be core-owned transport-neutral types. They must not expose NATS messages, JetStream metadata, Kafka records, offsets, or partition details.

For v1, `ProcessScenarioTrans(ctx, scenarioID, publisher)` depends on `TranslationRequestPublisher`. The NATS implementation satisfies that interface by publishing to JetStream and waiting for `PubAck`.

Reuse guidance for other components:
- EDS can later move from `nats.EDSBatchProcessor` to a core-owned batch handler type in `communication.go`
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

`{project}` is `SimulationExperiment.metadata.name`, which is already persisted as the project name.

### Stream

Use one JetStream stream for translator communication:

| Setting | Value |
| --- | --- |
| stream name | `cbse_translator` |
| stream subjects | `cbse.*.trans.request`, `cbse.*.trans.*.ready` |
| retention | `nats.WorkQueuePolicy` |
| storage | `nats.FileStorage` |
| discard policy | `nats.DiscardOld` |

`ensureTranslatorStream(js, cfg)` must mirror the EDS stream helper:
- look up the configured stream name
- create it when it does not exist
- fail on lookup errors other than `nats.ErrStreamNotFound`
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

`StartTranslatorComms(ctx)` must mirror the EDS startup shape and satisfy the core translator consumer interface:
1. read config with `loadTranslatorConfig`
2. verify the shared NATS connection exists and is connected
3. create a JetStream context
4. call `ensureTranslatorStream`
5. subscribe to the ready wildcard with `QueueSubscribe`, the configured queue group, durable name, manual ACK, ACK wait, and max ACK pending
6. flush the NATS connection
7. log the ready subject, stream, durable consumer, and queue group
8. on context cancellation, do not explicitly unsubscribe the JetStream durable subscription, matching the EDS rolling-restart behavior

`PublishTranslationRequest` must satisfy `TranslationRequestPublisher`. It must publish to JetStream and return nil only after a successful `PubAck`, which is the v1 implementation of transport publish confirmation.

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
- project subject tokens are normalized with the existing EDS-style `sanitizeSubjectToken`
- wildcard subjects may be configured directly and are not derived from templates at runtime

`SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS` is parsed by `loadTranslatorMaxAttempts()`, which requires a positive integer, logs invalid values, and falls back to `3`. Both `loadTranslatorConfig()` and `loadTranslatorHandoffConfig()` must call this shared helper so ready handling, handoff publishing, and unpublished-claim recovery use the same value.

`SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS` is included in `transConfig` so `handleTranslatorReady` can apply failed-reschedule policy for empty-image ready messages.

## Message Schemas

All payloads are JSON.
No extra JSON fields are allowed. Decoding must use `json.Decoder.DisallowUnknownFields` for consumed payloads and must reject trailing JSON tokens after the first decoded object.

### Translation Request

Published by SM to `cbse.{project}.trans.request`.

```json
{
  "id": 42,
  "project": "demo-project",
  "project_id": 7,
  "translation_attempt": 1,
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
| `translation_attempt` | integer | yes | incremented `scenario_status.translation_attempts` value returned by the successful claim |
| `recipe_info` | object or null | no | copied from `scenario_status.recipe_info` |
| `confidence_metric` | number or null | no | copied from `scenario_status.confidence_metric` |

### Ready Message

Published by Translator to `cbse.{project}.trans.{scenario_id}.ready`.

```json
{
  "id": 42,
  "project": "demo-project",
  "project_id": 7,
  "translation_attempt": 1,
  "container_image": "registry.example.com/team/scenario:abc123"
}
```

Field contract:

| Field | Type | Required | Notes |
| --- | --- | --- | --- |
| `id` | integer | yes | must match subject scenario id |
| `project` | string | yes | must match subject project token |
| `project_id` | integer | yes | must match DB row |
| `translation_attempt` | integer | yes | must match current DB `translation_attempts` value |
| `container_image` | string | yes | image to persist into `scenario_status.container_image` |

## Subject And Payload Validation

SM must validate all of the following before updating the DB:

1. subject matches the expected `ready` pattern
2. payload JSON decodes successfully with unknown fields rejected
3. string fields are trimmed before validation
4. payload `id`, `project_id`, and `translation_attempt` are positive integers
5. payload `project` is not empty after trimming whitespace
6. subject `{project}` equals `sanitizeSubjectToken(payload.project)`
7. subject `{scenario_id}` equals payload `id`
8. payload `container_image` is not empty after trimming whitespace
9. DB row exists
10. DB row `project_id` equals payload `project_id`
11. DB row `translation_attempts` equals payload `translation_attempt`

Do not validate container image syntax beyond requiring a non-empty string in v1. Container image references may come from different registries and formats, and stricter syntax checks are intentionally deferred.

If validation fails because the message is malformed, contains unknown fields, contains non-positive IDs, references a nonexistent scenario, or references an old `translation_attempt`:
- log the reason
- ACK the message

Reason: these are poison messages, not transient failures.

If `container_image` is empty, classify the ready message as a poison Translator response for the current attempt after SM has validated enough information to identify the exact row:
1. validate subject shape, strict JSON shape, positive `id`, positive `project_id`, positive `translation_attempt`, non-empty project, subject/project match, and subject/scenario match
2. load the DB row and validate project id and attempt
3. call `MarkScenarioTranslationAttemptFailed(ctx, scenarioID, attempt, maxAttempts)`
4. if attempts remain, transition the exact `Scheduled` attempt back to `Created`
5. if attempts are exhausted, transition the exact `Scheduled` attempt to `Failed`
6. do not increment `translation_attempts` during this recovery update; the current attempt was already counted when the scenario was claimed
7. if the scenario returns to `Created`, the next claim increments `translation_attempts` for the next attempt
8. ACK the message after DB recovery succeeds
9. NAK only if that DB recovery update fails transiently

If validation fails due to transient DB or infrastructure errors:
- NAK the message

## Ready Handling And Idempotency

When SM consumes a ready message:
1. load the scenario row by `id`
2. classify missing rows and project mismatches as poison and ACK
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

## Startup Integration

`RunScenarioManager` must start the configured communication adapters in addition to the SimulationExperiment informer. NATS/JetStream is the default and only required v1 adapter.

Startup order:
1. start the SimulationExperiment informer
2. start EDS communication
3. initialize the v1 NATS/JetStream translator communication adapter
4. start Translator ready-message consumption through the core `TranslatorReadyConsumer` interface

Translator communication startup is a required dependency. Failure to initialize it must terminate the process.

The future orchestration loop in `scenario_manager.go` is out of scope for this implementation step.

## Required Functions

### `scenario-manager/internal/core/communication.go`

This file must define the reusable communication boundary for core-owned orchestration. It must be in package `core`.

Required translator interfaces:
- `TranslationRequestPublisher`
- `TranslatorReadyConsumer`
- `TranslatorReadyHandler`

Required transport-neutral translator message/result types:
- `TranslatorReadyMessage`
- `TranslatorReadyHandlingResult`

The types in this file must not depend on NATS, JetStream, Kafka, or any broker-specific client type.

EDS and future component communication should reuse this pattern. Do not refactor existing EDS code as part of this translator implementation, but future EDS modularization should move the current `nats.EDSBatchProcessor` style callback into a core-owned handler type.

### `scenario-manager/internal/core/translator_handoff.go`

- `ProcessScenarioTrans(ctx context.Context, scenarioID int, publisher TranslationRequestPublisher) error`
- `RecoverUnpublishedTranslationClaim(ctx context.Context, scenarioID int) (stateChanged bool, finalState string, err error)`
- `loadTranslatorHandoffConfig() translatorHandoffConfig`
- `loadTranslatorMaxAttempts() int`

`translatorHandoffConfig` should contain:
- `MaxAttempts int`
- `PublishRecoveryTimeout time.Duration`

`ProcessScenarioTrans` must call `publisher.PublishTranslationRequest(ctx, *claimedScenario)` after the DB claim commits. If the publisher returns an error, execute the existing publish-failure recovery path. If the publisher returns nil, mark the exact claimed attempt as published.

### `scenario-manager/internal/coredb/scenario_status.go`

- `ClaimScenarioForTranslation(ctx context.Context, scenarioID int) (*ScenarioForTranslation, error)`
- `MarkScenarioTranslationRequestPublished(ctx context.Context, scenarioID int, attempt int) (bool, error)`
- `MarkScenarioTranslationAttemptFailed(ctx context.Context, scenarioID int, attempt int, maxAttempts int) (stateChanged bool, finalState string, err error)`
- `MarkScenarioTranslationPublishFailed(ctx context.Context, scenarioID int, attempt int, maxAttempts int) (stateChanged bool, finalState string, err error)`
- `MarkScenarioTranslatorReady(ctx context.Context, scenarioID int, projectID int, translationAttempt int, containerImage string) (TranslatorReadyResult, error)`
- `RecoverUnpublishedTranslationClaim(ctx context.Context, scenarioID int, claimedBefore time.Time, maxAttempts int) (stateChanged bool, finalState string, err error)`

`ScenarioForTranslation` should be defined in `coredb` and contain:
- `ID int`
- `Project string`
- `ProjectID int`
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
- `TranslatorReadyPoison`: row missing or project mismatch

Errors from `MarkScenarioTranslatorReady` are reserved for transient DB or infrastructure failures.

Ready handling must classify `TranslatorReadyApplied`, `TranslatorReadyDuplicate`, `TranslatorReadyConflict`, `TranslatorReadyIgnoredStale`, and `TranslatorReadyPoison` as successful terminal handling outcomes. The NATS adapter maps these outcomes to ACK. It maps transient errors to NAK.

Coredb transition semantics:
- public helpers reject non-positive IDs or attempts with an error before executing SQL
- `ClaimScenarioForTranslation` returns `(nil, nil)` when the row is missing or not `Created`
- `MarkScenarioTranslationRequestPublished` updates only `id = scenarioID`, `state = 'Scheduled'`, `translation_attempts = attempt`, and `translation_request_published_at IS NULL`
- `MarkScenarioTranslationAttemptFailed` updates only `id = scenarioID`, `state = 'Scheduled'`, and `translation_attempts = attempt`
- `MarkScenarioTranslationPublishFailed` additionally requires `translation_request_published_at IS NULL`
- recovery helpers return counts and do not treat zero affected rows as errors

### `scenario-manager/internal/nats/trans_com.go`

- `StartTranslatorComms(ctx context.Context) error`
- `StartTranslatorReadyConsumer(ctx context.Context, handler core.TranslatorReadyHandler) error`
- `PublishTranslationRequest(ctx context.Context, scenario coredb.ScenarioForTranslation) error`
- `handleTranslatorReady(ctx context.Context, msg *nats.Msg, cfg transConfig)`
- `ensureTranslatorStream(js nats.JetStreamContext, cfg transConfig) error`
- `loadTranslatorConfig() transConfig`

The NATS translator communication implementation must satisfy the core `TranslationRequestPublisher` and `TranslatorReadyConsumer` interfaces.

`StartTranslatorComms` may remain as the default startup helper used by `RunScenarioManager`, but the adapter must also expose the interface-shaped ready consumer method so core orchestration is not tied to the NATS helper name.

`PublishTranslationRequest` must use JetStream publish semantics, wait for the server `PubAck`, and return nil only after a successful `PubAck`. If JetStream does not return a successful `PubAck`, return an error and do not mark `translation_request_published_at`.

`transConfig` should include:
- request subject template
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
- heartbeat request/reply between SM and Translator
- dynamic resource-aware scheduling
- batching multiple translation requests into one message
- Translator error response messages beyond absence of ready

## Completion Criteria

The implementation is complete when SM can execute the translator handoff for a supplied scenario id through a `TranslationRequestPublisher`, persist successful transport publish confirmation, consume ready messages idempotently through the configured adapter, apply failed-reschedule policy for publish failures and empty-image ready messages, recover a supplied unpublished claim by scenario id, and leave published active translations untouched until Translator replies or a future protocol is added.
