# Specification for a Basic Scenario Selection Logic
This Document has the following structure:

1. Mission Statement -- Explains the intent and attitude of the work
2. Scope -- Explains the boundaries of the work; the goals, changes and scope in a general way
3. Change Location -- Describes the locations and files that need to be changed or created
4. Logic Description -- Describes the logic implemented in the newly created or changed files


## Mission Statement
This specification describes the first basic scenario selection logic for the Scenario Manager.

The goal is to add a simple, reliable first version of the decision step that chooses which stored scenario should be processed next. This is not meant to be the final or most intelligent scheduling logic. It is meant to create a clear starting point that works, is easy to understand, and can later be replaced by more advanced selection strategies.

The implementation should favor clarity over cleverness. The AI coding agent should keep the logic small, explicit, modular, and close to the existing Scenario Manager structure. It should reuse the current translator handoff workflow instead of redesigning it, avoid advanced scheduling behavior, and leave the code in a shape where future scenario selection logic can be swapped in without disturbing EDS ingestion or Translator communication.


## Scope

This change adds the first Basic Scenario Selection Logic (BSSL) to the Scenario Manager. BSSL is one process-wide worker that repeatedly finds at most one eligible scenario and advances one lifecycle step.

The words **must** and **must not** describe required behavior. **May** describes behavior that is optional without changing this contract.

### Terms

| Term | Meaning in this specification |
| --- | --- |
| Ordinary candidate | A positive-ID row currently in `Created`, `StartingRunners`, or `PostProcessing`. |
| Recovery candidate | A positive-ID `Scheduled` row whose translation publish confirmation is missing, whose claim is old enough for recovery, and whose translation attempt is returned with the row. |
| Claim | A durable database state change completed before a component performs a possible external side effect. |
| Guarded transition | One atomic update that succeeds only while the row still has the expected ID, state, and any workflow-specific attempt or age values. |
| Stale result | A guarded operation that changes zero rows because another worker or newer workflow step changed the row first. It is a normal no-op, not a database failure. |
| Placeholder | Local scaffold code that logs and returns a fixed result without calling the future runner or PostProcessingService. |
| Terminal state | `Finished` or `Failed`; BSSL never selects these rows. |
| Iteration | One recovery check followed, only when no recovery candidate exists, by one ordinary lookup and dispatch. |

The existing code and database column use the word “unpublished.” More precisely, `translation_request_published_at IS NULL` means that Scenario Manager did not record publish confirmation. It does not prove that the broker never accepted the message. This specification therefore uses “unconfirmed publish” when explaining the behavior, while retaining existing function and column names.

### Lifecycle and Ownership

| State | Meaning | BSSL behavior | Owner of the next real step |
| --- | --- | --- | --- |
| `Created` | Eligible for a Translator claim. It may be newly ingested or returned for another translation attempt. | Ordinary candidate; delegate to the existing Translator handoff. | Existing Translator handoff. |
| `Scheduled` | Translation is in flight. | Never an ordinary candidate. Only a stale unconfirmed publish may enter the narrow recovery path. | Translator handoff and Translator ready handling. |
| `StartingRunners` | Translation supplied the runner image. | Ordinary candidate; claim it as `InProcessing`, then invoke the local runner placeholder. | Future runner integration. |
| `InProcessing` | Runner work is considered in flight. | Ignored. BSSL does not recover or advance it. | Future runner execution and completion logic. |
| `PostProcessing` | Runner completion logic requested a confidence decision. | Ordinary candidate; invoke the local post-processing placeholder and apply its result. | BSSL placeholder now; future PostProcessingService later. |
| `Finished` | Terminal success. | Ignored. | No next step. |
| `Failed` | Terminal failure. | Ignored. | No next step. |
| Any unknown value | Invalid or future state not known to BSSL. | Ignored indefinitely and never changed. | Operator repair or a future implementation. |

The lifecycle edges relevant to BSSL are:

```text
Created --existing Translator claim--> Scheduled
Scheduled --existing ready handling--> StartingRunners
Scheduled --stale unconfirmed recovery--> Created or Failed
StartingRunners --BSSL runner claim--> InProcessing
InProcessing --future runner completion--> PostProcessing
PostProcessing --confidence reached--> Finished
PostProcessing --more runs required--> StartingRunners
InProcessing or PostProcessing --owned placeholder failure--> Failed
```

### Selection Boundaries

Ordinary selection is one global lookup across the resolved Scenario Status table. It does not join the Project table and does not filter by project or `SimulationExperiment` phase. Rows from empty, pending, in-progress, completed, failed, or error projects participate equally.

The ordinary lookup selects the lowest currently visible positive `scenario_status.id` across `Created`, `StartingRunners`, and `PostProcessing`. “FIFO” in this specification means this numeric ID order:

- gaps caused by deleted rows are allowed;
- it is not strict wall-clock or transaction-commit order;
- an older `PostProcessing` row precedes a newer `Created` row;
- project status, project name, `priority`, timestamps, repetition counts, confidence, image, and runtime estimates do not affect the order.

A recovery candidate always takes precedence over all ordinary work, regardless of the two candidates’ IDs. One iteration considers at most one recovery candidate or one ordinary candidate. A stale result, lost race, or handler error consumes the iteration; BSSL must not fall through to a replacement row in the same iteration.

The fixed five-second throttle limits one Scenario Manager process to at most 12 candidate actions per minute when operations complete quickly. Completing 1,000 candidate actions therefore takes at least about 83 minutes. One scenario may require several actions, and this BSSL version does not complete the full real lifecycle. Multiple replicas remain correct, but they commonly discover the same lowest-ID row and therefore do not multiply throughput predictably.

This simple order intentionally permits head-of-line blocking:

- a repeatedly failing lowest-ID ordinary row can delay every higher ID;
- a continuing recovery backlog can delay all ordinary work;
- BSSL has no skip, quarantine, per-row delay, fairness, or project balancing rule;
- persistent invalid data requires operator repair.

BSSL does not limit the number of rows already in `Scheduled` or `InProcessing`. It performs no capacity check or downstream backpressure check before selecting another row.

### Placeholder Limitations

The runner and post-processing paths are lifecycle scaffolding, not end-to-end simulation execution:

- `StartingRunners -> InProcessing` occurs even though BSSL creates no Kubernetes Job or other runner.
- A successfully invoked runner placeholder means only that the local placeholder returned nil. It does not prove a runner exists.
- BSSL does not recover `InProcessing` after restart or cancellation. Such rows remain parked until future runner logic is implemented.
- The production post-processing placeholder reads no confidence or repetition data and returns `true, nil`.
- A directly encountered `PostProcessing` row therefore becomes `Finished` without a real confidence calculation.
- `number_of_reps`, `number_of_computed_reps`, and `confidence_metric` remain unchanged.
- These synthetic `InProcessing` and `Finished` outcomes must not be presented as evidence of real simulation execution.

Enabling BSSL immediately acts on all pre-existing matching rows. There is no feature flag or staged activation.

### Out of Scope

BSSL must not add:

- resource-aware, priority-based, project-aware, or runtime-based scheduling;
- batching, concurrent per-process workers, leader election, or replica coordination;
- real runner creation, runner monitoring, or `InProcessing -> PostProcessing` logic;
- a real PostProcessingService call or durable post-processing claim;
- recovery for published `Scheduled` work;
- heartbeats, Translator failure messages, cancellation protocols, or an outbox;
- new metrics, health endpoints, failure-reason columns, or log-rate limiting;
- automatic migration, constraint repair, or backfill of an existing legacy Scenario Status table;
- changes to EDS, Experiment Operator behavior, Kubernetes API types, NATS subjects, payloads, stream semantics, acknowledgements, or external APIs.

## Change Location

All implementation changes must remain inside `scenario-manager`.

1. Create `scenario-manager/internal/core/selector.go` for the selector instance, its private dependencies, fixed timing policy, joinable worker, recovery-first iteration, state dispatch, and placeholders.
2. Create focused selector tests in `scenario-manager/internal/core/selector_test.go`.
3. Update `scenario-manager/internal/core/scenario_manager.go` to start Translator communication, start BSSL, wait for its completion during shutdown, and update the function comments and ready/shutdown logs.
4. Update `scenario-manager/internal/core/translator_handoff.go` so stale unconfirmed recovery accepts the discovered translation attempt and exact caller-provided cutoff. Split its configuration use so Translator handoff and ready/recovery logic load only the max-attempt policy; selector startup alone loads the publish recovery timeout.
5. Extend `scenario-manager/internal/coredb/scenario_status.go` with both candidate lookups, attempt-exact recovery, and guarded state helpers. Update its file comments and make the `ScenarioStateFailed` comment describe workflow-generic terminal failure.
6. Extend `scenario-manager/internal/coredb/schema.go` with fail-fast legacy-schema validation and the two table-specific partial indexes.
7. Extend the existing environment-gated Core DB integration tests. Do not add a SQL-mocking dependency.

The implementation must reuse `scenario-manager/internal/nats/trans_com.go` without changing its subjects, message bodies, stream configuration, publish confirmation, ACK/NAK behavior, or public interfaces.

## Logic Description

### Fixed Configuration

| Setting | Required value | Source and behavior |
| --- | --- | --- |
| Selector delay | `5s` | Private constant. No new environment variable. |
| Iteration timeout | `30s` | Private constant. No new environment variable. |
| Unconfirmed-publish recovery timeout | Default `1m` | Existing `SCENARIO_MANAGER_TRANS_PUBLISH_RECOVERY_TIMEOUT`. The selector loads it once during startup; invalid or non-positive values use the existing default. It is not reloaded or relogged per selector iteration. |
| Maximum translation attempts | Default `3` | Existing `SCENARIO_MANAGER_TRANS_MAX_ATTEMPTS`. Invalid or non-positive values use the existing fallback behavior. |

The selector must call `translatorconfig.LoadPublishRecoveryTimeout()` once during startup and retain the result. Unset configuration silently uses the default; invalid or non-positive configuration logs once during that selector startup and uses the default. No Translator handoff, ready handler, recovery helper, or selector iteration may reload this timeout. Those existing workflows may continue loading only the max-attempt policy when they need it.

Each recovery cutoff is calculated by the Scenario Manager process and compared with `updated_at`, which PostgreSQL writes with `NOW()`. The deployment therefore assumes the application and database clocks are reasonably synchronized.

### Startup Sequence

`RunScenarioManager(ctx)` must start dependencies in this order:

1. Start the `SimulationExperiment` informer with `handleSimulationExperimentEvent`.
2. Start EDS communication with the existing sequential EDS batch processor.
3. Call `nats.StartTranslatorComms(ctx, HandleTranslatorReady)`, which initializes the adapter and starts its ready consumer.
4. Reuse the returned `*nats.TranslatorComms` as the `communication.TranslationRequestPublisher`.
5. Start the joinable BSSL worker.
6. Log that Scenario Manager is ready and that the BSSL worker has started.
7. Wait for `ctx.Done()`.
8. Wait for the BSSL completion channel to close.
9. Log final Scenario Manager shutdown.

The completion channel joins only the new BSSL worker. The informer and existing EDS and Translator consumers retain their current context-driven lifecycle and are not given new join handles in this change.

Informer, EDS, Translator communication, and selector startup errors are fatal and must preserve the existing startup style. A database error from the worker’s first iteration is not a startup error.

The selector startup API must be:

```go
func StartBasicScenarioSelector(
	ctx context.Context,
	publisher communication.TranslationRequestPublisher,
) (<-chan struct{}, error)
```

`StartBasicScenarioSelector` must:

1. Reject a nil `ctx`.
2. Reject a context whose `Err()` is already non-nil.
3. Reject a nil publisher interface.
4. Load and retain the publish recovery timeout once.
5. Construct one selector instance with production dependencies.
6. Start exactly one background worker for that successful call.
7. Return a receive-only completion channel.
8. Close the completion channel exactly once when the worker exits.

If validation fails, the function must return a nil completion channel and an error without starting a goroutine. The function is not idempotent and must not implement process-global singleton state. `RunScenarioManager` is responsible for calling it exactly once.

The ready log confirms only that the worker was launched. The first iteration runs inside the worker and may fail without terminating Scenario Manager.

### Private Selector and Testability

The core package must use one unexported selector instance and one unexported dependency bundle. The selector instance must retain:

- the publisher;
- the loaded recovery timeout;
- fixed selector timing configuration;
- the production dependency bundle.

The dependency bundle must provide:

- a clock used to calculate `claimedBefore`;
- a context-aware wait hook used between completed iterations;
- recovery discovery and exact recovery functions;
- the ordinary candidate lookup;
- the existing translation handoff;
- all guarded state-update functions;
- the runner and post-processing placeholders.

The wait dependency must have the logical shape `wait(ctx context.Context, delay time.Duration) error`. The worker must call it with the long-lived root worker context and the fixed selector delay, never with the iteration child context that was just cancelled.

Production dependencies delegate to the existing core and Core DB functions. Tests supply instance-scoped fakes and a caller-controlled wait channel. The implementation must not use mutable package-global function variables for test replacement.

Every production or fake dependency invoked by the worker must honor context cancellation. The 30-second deadline bounds an iteration only when the active dependency returns after its context is cancelled.

The per-iteration method must provide this behavior:

```go
func (s *basicScenarioSelector) processNextScenario(ctx context.Context) error
```

The concrete private type and field names may differ, but they must preserve this instance-scoped design and behavior.

### Worker Timing, Timeout, and Shutdown

The worker must run serially:

1. Start the first iteration immediately inside the worker.
2. Derive a child context with a `30s` timeout for that iteration.
3. Cancel the child context after the iteration returns.
4. Log a non-cancellation iteration error once at the worker boundary.
5. Using the root worker context, wait five full seconds after the iteration completes, fails, finds no work, or times out.
6. Start the next iteration only after that wait.

The delay is measured after completion, not on a wall-clock ticker. Missed intervals are not queued or replayed. Iterations must never overlap, and the worker must not launch a goroutine per iteration.

If the root context is cancelled:

- during the wait, return immediately;
- during an iteration, propagate cancellation through the child context and wait for the context-aware call to return;
- do not start another iteration;
- close the completion channel before `RunScenarioManager` logs final shutdown.

`context.Canceled` and `context.DeadlineExceeded` are workflow cancellation, not scenario failure. A timeout may leave a durable claim for later component-owned recovery, but BSSL must not add an independent `Failed` transition.

### Per-Iteration Algorithm

One iteration must execute this exact order:

1. If `ctx.Err()` is non-nil, return without querying.
2. Calculate one `claimedBefore` value as `now().Add(-publishRecoveryTimeout)`.
3. Call `NextStaleUnpublishedTranslationClaim(ctx, claimedBefore)`.
4. If discovery returns an error, end the iteration. Do not run ordinary lookup.
5. Re-check `ctx.Err()`.
6. If a recovery candidate exists, call exact recovery with its ID, its discovered translation attempt, and the same `claimedBefore`; then end the iteration for success, stale result, cancellation, or error.
7. Only when no recovery candidate exists, call `NextActionableScenario(ctx)`.
8. If ordinary lookup returns an error or no candidate, end the iteration.
9. Re-check `ctx.Err()` before starting the state handler.
10. Dispatch exactly one handler for `Created`, `StartingRunners`, or `PostProcessing`.
11. If an unexpected state reaches dispatch, log it and leave the row unchanged.
12. Return after that handler; do not select another row in the same iteration.

An idle iteration must not emit a log line.

### Ordinary Candidate Lookup

Add this exact projection and public Core DB helper:

```go
type ScenarioActionCandidate struct {
	ID    int
	State string
}

func NextActionableScenario(ctx context.Context) (*ScenarioActionCandidate, error)
```

The helper must validate a non-nil context before accessing the DB pool. It must execute this logical query against the resolved table:

```sql
SELECT id, state
FROM <resolved_scenario_status_table>
WHERE id > 0
  AND state IN ('Created', 'StartingRunners', 'PostProcessing')
ORDER BY id ASC
LIMIT 1;
```

The canonical states must remain literal, compile-controlled SQL values so PostgreSQL can match the query to the partial-index predicate even when it uses a generic prepared plan.

The helper must:

- use the existing resolved Scenario Status table name;
- select only `id` and `state`;
- return `nil, nil` for `sql.ErrNoRows`;
- perform no update, transaction, `FOR UPDATE`, or other lock;
- wrap DB errors with the operation name but not log them.

### Recovery Candidate Lookup

Add this exact projection and public Core DB helper:

```go
type TranslationRecoveryCandidate struct {
	ID                 int
	TranslationAttempt int
}

func NextStaleUnpublishedTranslationClaim(
	ctx context.Context,
	claimedBefore time.Time,
) (*TranslationRecoveryCandidate, error)
```

The helper must validate a non-nil context and reject a zero `claimedBefore` value before accessing the DB pool. It must execute this logical query against the resolved table:

```sql
SELECT id, translation_attempts
FROM <resolved_scenario_status_table>
WHERE id > 0
  AND state = 'Scheduled'
  AND translation_request_published_at IS NULL
  AND updated_at < claimedBefore
ORDER BY id ASC
LIMIT 1;
```

The strict `<` boundary means a row whose `updated_at` equals the cutoff is not yet stale.

The helper must:

- return `nil, nil` for `sql.ErrNoRows`;
- perform no update, transaction, `FOR UPDATE`, or other lock;
- return the stored attempt with the ID;
- wrap DB errors but not log them.

A `Scheduled` row is expected to have a positive translation attempt because the existing claim increments the counter before entering `Scheduled`. Exact recovery must reject a non-positive expected attempt. Such invalid persistent data is an operator-repair condition.

### Attempt-Exact Recovery

Change the existing core recovery helper to accept the discovered attempt and cutoff:

```go
func RecoverUnpublishedTranslationClaim(
	ctx context.Context,
	scenarioID int,
	expectedAttempt int,
	claimedBefore time.Time,
) (stateChanged bool, finalState string, err error)
```

Change the Core DB helper accordingly while retaining the maximum-attempt argument owned by the core workflow:

```go
func RecoverUnpublishedTranslationClaim(
	ctx context.Context,
	scenarioID int,
	expectedAttempt int,
	claimedBefore time.Time,
	maxAttempts int,
) (stateChanged bool, finalState string, err error)
```

The core helper must use the caller’s exact `claimedBefore`; it must not reload the recovery timeout or call `time.Now()`. It may continue loading the existing maximum-attempt policy, but it must use a max-attempt-only loader rather than the current combined loader. `ProcessScenarioTrans` and `HandleTranslatorReady` must likewise load only the max-attempt policy, because neither operation uses the recovery timeout.

Both helpers must reject nil context, non-positive IDs, non-positive expected attempts, a zero cutoff, and non-positive maximum attempts before executing SQL.

The Core DB update must guard:

```sql
id = scenarioID
AND state = 'Scheduled'
AND translation_attempts = expectedAttempt
AND translation_request_published_at IS NULL
AND updated_at < claimedBefore
```

It must atomically set:

- `state = 'Created'` when `translation_attempts < maxAttempts`;
- `state = 'Failed'` when `translation_attempts >= maxAttempts`;
- `updated_at = NOW()`.

It must not change `translation_attempts`, the publish marker, or any other column.

The result contract is:

- one changed row: return `true`, the resulting state, and nil;
- zero rows: return `false, "", nil`; this is a stale or already-handled race;
- SQL failure: return `false, "", error`.

A recovery candidate consumes the iteration even when the update changes zero rows or returns an error. A successful `Scheduled -> Created` recovery must not translate the row in the same iteration.

Published `Scheduled` rows, where the marker is non-null, must never be recovered by BSSL. They can remain stuck indefinitely until a future heartbeat, explicit failure, cancellation, or published-work timeout protocol exists.

### Delivery and Crash Windows

Translation delivery is at-least-once across the boundary between JetStream publish confirmation and the database publish marker. The request already carries both scenario ID and translation attempt. Late ready messages from an older attempt must remain harmless because the existing ready workflow compares the message attempt with the current DB attempt.

| Interruption point | Durable state after restart | Required BSSL behavior |
| --- | --- | --- |
| Before `Created -> Scheduled` commits | `Created` | A later ordinary iteration may claim it. |
| After the claim commits but before publish | `Scheduled`, marker null | Recover after the configured timeout, guarded by the exact attempt and cutoff. |
| After JetStream PubAck but before marker persistence | `Scheduled`, marker null although the message may exist | Recovery may create a later attempt and duplicate Translator work. Old ready messages remain attempt-stale. |
| After marker persistence | `Scheduled`, marker non-null | BSSL never recovers it, even if Translator never replies. |
| After ready handling | `StartingRunners` | A later ordinary iteration claims runner startup. |
| After `StartingRunners -> InProcessing` but before placeholder completion | `InProcessing` | BSSL leaves it parked; future runner recovery must own this case. |
| After post-processing placeholder return but before its guarded update | `PostProcessing` | A later iteration safely repeats the side-effect-free placeholder. |

No exactly-once claim is made. This BSSL step must not introduce an outbox or change the Translator wire contract.

### `Created` Handling

For a `Created` candidate, call:

```go
ProcessScenarioTrans(ctx, candidate.ID, publisher)
```

The selector must not duplicate or bypass any Translator behavior. `ProcessScenarioTrans` continues to own:

- the exact `Created -> Scheduled` claim;
- translation-attempt increment;
- request publication;
- publish-marker persistence;
- publish-failure recovery;
- maximum-attempt decisions.

A nil result completes the iteration. If the row became unclaimable after lookup, the existing handoff returns nil and that stale claim still consumes the iteration.

Any returned error must end the iteration and be logged by the worker. The selector must never call `MarkScenarioFailedFrom` for a Translator handoff error. The existing Translator workflow alone decides whether the row remains `Scheduled`, returns to `Created`, or becomes `Failed`.

### `StartingRunners` Handling

First claim the row:

```go
MarkScenarioInProcessing(ctx, candidate.ID)
```

Required outcomes:

- `false, nil`: log a stale transition and end the iteration without invoking the placeholder;
- `false, error`: end the iteration without invoking the placeholder or failing the scenario;
- `true, nil`: re-check `ctx.Err()`, then invoke the placeholder only while the context remains active.

The placeholder contract is:

```go
createSimulationRunnerJob(ctx context.Context, scenarioID int) error
```

For BSSL it must:

1. Reject nil context and non-positive IDs.
2. Return the context error when the context is already cancelled or timed out.
3. Log that the synthetic runner placeholder was requested.
4. Create no Job or external resource.
5. Return nil.

When the placeholder returns nil, leave the row in `InProcessing`. This means only that placeholder invocation completed.

When the placeholder returns an error:

1. Ignore it as a scenario failure if the context is cancelled or timed out.
2. Otherwise call `MarkScenarioFailedFrom(ctx, candidate.ID, coredb.ScenarioStateInProcessing)`.
3. Treat a zero-row failure write as stale and finish the iteration.
4. If the failure write itself errors, preserve both the placeholder error and persistence error in the returned/logged error; do not retry in the same iteration.

BSSL must not move `InProcessing` to `PostProcessing`. Future real runner integration must retain claim-before-side-effect ordering, use an idempotent external identity such as a deterministic Job name, and add reconciliation for parked `InProcessing` rows.

### `PostProcessing` Handling

Call:

```go
doPostProcessing(ctx context.Context, scenarioID int) (confidenceReached bool, err error)
```

For BSSL the placeholder must:

1. Reject nil context and non-positive IDs.
2. Return the context error when already cancelled or timed out.
3. Log that synthetic post-processing was requested.
4. Make no external call and read no confidence or repetition data.
5. Return `true, nil`.

The handler must inspect `err` before the boolean. If `err != nil`, `confidenceReached` must be ignored even when true.

For an error:

- if the context is cancelled or timed out, make no state change;
- otherwise call `MarkScenarioFailedFrom(ctx, candidate.ID, coredb.ScenarioStatePostProcessing)`;
- a stale zero-row result ends the iteration;
- if the failure write errors, preserve both errors and do not retry in the same iteration.

For a nil error:

1. Re-check `ctx.Err()` before writing a result.
2. If `confidenceReached == true`, call `MarkScenarioFinished`.
3. If `confidenceReached == false`, call `MarkScenarioRequiresMoreRuns`.
4. Treat a zero-row result as stale.
5. Return a SQL error without marking the scenario `Failed`; the row remains `PostProcessing` for a later iteration.

The false branch must only move the row to `StartingRunners`. It must not invoke runner startup in the same iteration.

The future real PostProcessingService integration must add a durable claim token or canonical in-flight state before making an external side-effecting call.

### Guarded State Helpers

Add these helpers:

```go
func MarkScenarioInProcessing(ctx context.Context, scenarioID int) (bool, error)
func MarkScenarioFinished(ctx context.Context, scenarioID int) (bool, error)
func MarkScenarioRequiresMoreRuns(ctx context.Context, scenarioID int) (bool, error)
func MarkScenarioFailedFrom(ctx context.Context, scenarioID int, expectedState string) (bool, error)
```

Every helper must validate arguments before accessing the DB pool. A nil context or non-positive scenario ID is invalid.

`MarkScenarioFailedFrom` must use this exact, case-sensitive allowlist:

```text
Created
Scheduled
StartingRunners
InProcessing
PostProcessing
```

It must reject empty strings, whitespace variants, unknown states, `Finished`, and `Failed`. Callers must pass the existing Core DB state constants rather than repeating string literals. Although the helper recognizes every canonical non-terminal state, BSSL may call it only with `InProcessing` or `PostProcessing`.

Each helper must execute one atomic `UPDATE` with an ID and expected-state predicate. It must not perform a preceding `SELECT`, use a transaction, or lock a row.

| Helper | Required transition |
| --- | --- |
| `MarkScenarioInProcessing` | `StartingRunners -> InProcessing` |
| `MarkScenarioFinished` | `PostProcessing -> Finished` |
| `MarkScenarioRequiresMoreRuns` | `PostProcessing -> StartingRunners` |
| `MarkScenarioFailedFrom` | `expectedState -> Failed` |

Every successful update must change only:

```sql
state = <new state>,
updated_at = NOW()
```

It must leave project, creation time, priority, repetitions, translation attempts, publish marker, recipe, container image, and confidence unchanged.

All helpers return:

- `true, nil` for one changed row;
- `false, nil` for a stale zero-row result;
- `false, error` for SQL or result-inspection failure.

Core DB helpers must wrap errors and must not log. The core selector owns logs and must not retry a guarded update within the same iteration.

### Failure and Cancellation Rules

| Condition | Required outcome |
| --- | --- |
| Recovery discovery or exact recovery error | Log operational error, end iteration, do not run ordinary dispatch, do not fail a scenario. |
| Ordinary lookup error | Log operational error, end iteration, do not fail a scenario. |
| Translator handoff error | Let existing Translator workflow own state; never call generic failure helper. |
| Runner claim error or stale result | Do not invoke runner placeholder and do not fail the row. |
| Runner placeholder error with active context | Attempt guarded `InProcessing -> Failed`. |
| Post-processing placeholder error with active context | Attempt guarded `PostProcessing -> Failed`. |
| Successful-result transition error | Leave current state for later retry; do not convert the persistence error into `Failed`. |
| Any context cancellation or iteration deadline | Make no selector-owned failure decision. |
| Any stale zero-row transition | Log stale result and end iteration. |
| No candidate | Silent no-op. |

Before any selector-owned `Failed` write, check `ctx.Err()`. Persistence errors must never themselves trigger a second failure update.

### Logging

Use the existing Go logger with a consistent key-value style; do not add a logging dependency. Action and error logs must include, when available:

- `operation`;
- `scenario_id`;
- expected old state;
- intended or resulting state;
- translation attempt for recovery;
- an error class such as validation, DB, translation, placeholder, timeout, cancellation, or stale.

Do not assert exact log sentences in tests. Do not log recipe JSON, confidence inputs, credentials, raw broker payloads, or other sensitive data. Idle iterations must not log.

### Concurrency and Stale Rows

BSSL must remain correct when more than one Scenario Manager replica runs:

- each replica may read the same recovery or ordinary candidate;
- translation’s existing exact claim decides one winner for `Created`;
- exact ID, attempt, marker, and cutoff guards decide one recovery winner;
- `StartingRunners -> InProcessing` decides one runner-placeholder winner;
- multiple replicas may invoke the post-processing placeholder because it is side-effect-free, but only one guarded result transition wins;
- failure updates require the expected state and cannot overwrite a newer state;
- every losing replica consumes its current iteration and must not select another row.

The selector must hold no long-lived database or in-memory lock. Durable ownership exists only through database state and attempt guards. BSSL adds no leader election or cross-replica memory coordination.

### Schema Validation and Compatibility

`CheckingDependencies` already calls `coredb.EnsureTablesAvailable` before `RunScenarioManager`. BSSL must extend that existing startup path so invalid legacy schemas fail before the selector starts.

Schema setup must first determine whether the resolved Scenario Status table already exists:

- when it does not exist, create it once with the complete current schema, project foreign key, and constraints shown below;
- when it already exists, do not run the existing `ensureScenarioStatusProjectLink` mutation or any other `ALTER TABLE` repair against it; validate it read-only and fail startup when it is incompatible;
- after successful creation or validation, create the two additive BSSL indexes.

Existing Project-table maintenance may remain unchanged. The no-auto-migration rule in this section applies to the Scenario Status table.

The resolved table must satisfy this compatibility contract:

| Column | Compatible PostgreSQL type | Required constraint or default |
| --- | --- | --- |
| `id` | `INTEGER` | The only primary-key column, non-null, and generated by a serial- or identity-backed sequence. |
| `project_id` | `INTEGER` | Non-null and protected by a foreign key to the resolved Project table's `id`, with `ON DELETE CASCADE`. |
| `state` | `TEXT` | Non-null. |
| `created_at` | `TIMESTAMPTZ` | Non-null with a default whose normalized catalog expression is `now()` or `CURRENT_TIMESTAMP`. |
| `updated_at` | `TIMESTAMPTZ` | Non-null with a default whose normalized catalog expression is `now()` or `CURRENT_TIMESTAMP`. |
| `priority` | `INTEGER` | Non-null with default `0`. |
| `number_of_reps` | `INTEGER` | Non-null with default `0`. |
| `number_of_computed_reps` | `INTEGER` | Non-null with default `0`. |
| `translation_attempts` | `INTEGER` | Non-null with default `0`. |
| `translation_request_published_at` | `TIMESTAMPTZ` | Nullable. |
| `recipe_info` | `JSONB` | Nullable. |
| `container_image` | `TEXT` | Nullable. |
| `confidence_metric` | `DOUBLE PRECISION` | Nullable. |

The resolved Project table must provide a non-null integer primary-key `id` generated by a serial- or identity-backed sequence and a non-null text `project_name`, matching the existing insert, lookup, and Translator join. Existing maintenance may continue ensuring its `number_of_components INTEGER NOT NULL DEFAULT 0` and `status TEXT NOT NULL DEFAULT ''` columns.

Validation must also prove that every existing Scenario Status row has a non-null `project_id` and that no row references a missing Project row. A declared foreign key alone is insufficient because a nullable legacy column can still contain null values.

The validator must inspect PostgreSQL catalogs, not only column names:

- `pg_get_serial_sequence(resolvedScenarioTable, 'id')` and `pg_get_serial_sequence(resolvedProjectTable, 'id')` must each return a sequence for serial- or identity-backed generation;
- the primary key must contain only `id`;
- normalized timestamp default expressions may be `now()` or `CURRENT_TIMESTAMP`;
- normalized integer default expressions for the four default-zero columns must equal `0`, ignoring PostgreSQL-added casts and parentheses;
- the foreign key must map only `project_id` to the resolved Project table's `id` with `ON DELETE CASCADE`.

Do not automatically add, backfill, or reinterpret missing Translator-era columns. If validation fails, return a clear error that names the incompatible table and instructs the operator to migrate or recreate it. `EnsureTablesAvailable` must return false, and dependency startup must remain fatal.

The BSSL indexes are additive. No down-migration is required. Enabling an older binary after index creation remains valid.

### Partial Indexes

Define compile-controlled SQL predicate constants in `coredb` and reuse the same predicate text in the lookup queries and index definitions.

Create two normal, non-concurrent partial indexes after allowed Project-table maintenance and successful Scenario Status creation or read-only validation:

1. Ordinary actionable lookup:

```sql
CREATE INDEX IF NOT EXISTS scenario_status_actionable_id_idx
ON scenario_status (id)
WHERE state IN ('Created', 'StartingRunners', 'PostProcessing');
```

2. Unconfirmed translation recovery lookup:

```sql
CREATE INDEX IF NOT EXISTS scenario_status_unpublished_translation_id_idx
ON scenario_status (id)
WHERE state = 'Scheduled'
  AND translation_request_published_at IS NULL;
```

These examples use the default table. For a configured table such as `custom_status`, derive:

```text
custom_status_actionable_id_idx
custom_status_unpublished_translation_id_idx
```

Derive each name from the resolved table’s unqualified base name so default and custom tables can coexist in one schema. The index is created in the same schema as its table. The resolved table identifier itself must still be used in the `ON` clause.

The existing table-name environment variables remain trusted deployment configuration and must contain identifiers supported by the repository’s current SQL interpolation. Identifier hardening is outside this change.

Use ordinary `CREATE INDEX IF NOT EXISTS`, not `CREATE INDEX CONCURRENTLY`. Startup therefore requires DDL permission and index creation may briefly block writes. Any creation error is fatal. A same-named existing index is assumed to have the correct definition; BSSL does not repair or replace it automatically.

PostgreSQL maintains both indexes automatically. Do not add an application refresh task.

### Tests and Acceptance Criteria

#### Selector Unit Tests

Selector tests must always run without NATS, Kubernetes, or PostgreSQL. They must use the private dependency bundle, a fake publisher, a fake clock, and a caller-controlled wait hook. They must not use mutable global replacements or sleep for five real seconds.

Cover:

- nil context, already-cancelled context, and nil publisher startup rejection;
- no goroutine and nil completion channel after startup validation failure;
- immediate first iteration inside the worker;
- five-second post-completion delay through the controlled wait hook;
- fixed 30-second child-context timeout;
- root cancellation during an iteration and during the wait;
- completion-channel closure and joined shutdown;
- serial execution with no overlapping iterations;
- no candidate as a silent no-op;
- recovery discovery before ordinary lookup;
- no recovery candidate falling through to ordinary lookup;
- found, stale, failed, and successful recovery preventing ordinary lookup;
- use of one clock value and one cutoff for discovery and exact recovery;
- unexpected state causing no write;
- `Created` calling only the configured translation handoff;
- stale or failed runner claim never invoking the placeholder;
- successful runner claim preceding the placeholder;
- runner placeholder success leaving `InProcessing`;
- runner placeholder failure, failure-write stale result, and failure-write error;
- post-processing `true`, `false`, error, stale, and transition-error paths;
- `confidenceReached` ignored whenever the placeholder returns an error;
- cancellation or deadline before every selector-owned state write;
- no state handler choosing a replacement candidate in the same iteration;
- continuation on a later iteration after an ordinary operational error.

Do not test exact log strings.

#### PostgreSQL Integration Tests

Follow the existing environment-gated Core DB integration-test convention. Add no SQL-mocking library. Tests must use isolated custom tables or an isolated schema and clean up their own data so unrelated rows cannot change ID-order assertions.

Cover:

- global mixed-state ordering across multiple projects and project statuses;
- gaps in IDs, positive-ID filtering, ignored canonical states, and unknown states;
- `nil, nil` when each lookup has no candidate;
- nil-context and invalid-argument validation before DB access;
- exact cutoff boundary for recovery;
- lowest-ID recovery discovery returning the correct attempt;
- published, fresh, non-`Scheduled`, and stale-attempt protection;
- a discovered old attempt unable to recover a newer attempt;
- below-limit recovery to `Created` and at/above-limit recovery to `Failed`;
- a late ready message from a superseded attempt not changing the newer row;
- every guarded transition and zero-row stale result;
- the exact `MarkScenarioFailedFrom` allowlist;
- successful updates changing only `state` and `updated_at`;
- schema validation failures for missing required columns, nullable/invalid project links, and orphan rows;
- default and custom index names, columns, predicates, and target tables;
- repeated schema setup remaining idempotent.

#### Verification Commands

Run from `scenario-manager`:

```text
go test -race ./internal/core
go test ./internal/core ./internal/coredb
```

The PostgreSQL cases may skip when the existing Core DB environment is not configured.

Run `go test ./...` only in the intended Kubernetes test environment. Outside a cluster, the existing unrelated `internal/kube` test process exits through `KubeConnect` before it can skip; fixing that baseline behavior is outside BSSL.

### Completion Criteria

The implementation is complete when:

- startup initializes Translator communication and one joinable selector worker in the required order;
- shutdown cancels and joins that worker;
- every iteration has a 30-second deadline and is followed by a five-second delay with no overlap or catch-up;
- stale unconfirmed translation recovery uses one cutoff and the exact discovered attempt;
- recovery always precedes ordinary global lowest-ID selection;
- only one candidate is considered per iteration;
- `Created` reuses the existing Translator handoff unchanged;
- runner and post-processing paths exhibit the documented synthetic behavior;
- every state update is guarded and persistence failures never become independent scenario failures;
- schema incompatibility fails at startup with manual-remediation guidance;
- both table-specific partial indexes exist for default and custom tables;
- the focused unit and PostgreSQL integration tests cover the required behavior.
