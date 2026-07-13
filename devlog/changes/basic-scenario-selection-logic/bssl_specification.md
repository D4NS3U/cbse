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
This change adds the first basic scenario selection logic to the Scenario Manager.

Today, the Scenario Manager can receive scenarios from EDS and store them in the Core DB. It also already has a translator handoff workflow that can process one specific scenario when it is given a scenario ID. What is still missing is the simple decision step in between: choosing which stored scenario should be handled next.

The scope of this specification is to describe that first decision step. The Scenario Manager should be able to find scenarios that are ready for translation, select one suitable candidate, and pass it into the existing translator handoff flow. This first version is intentionally simple and predictable. It is meant to prove the basic workflow and create a clean place where more advanced selection logic can be added later.

The selection logic should focus only on states that this first basic Scenario Manager loop can actively move forward: `Created`, `StartingRunners`, and `PostProcessing`. A `Created` scenario has been received and stored, but has not yet been sent to the Translator. Once such a scenario is selected, the existing translator handoff is responsible for claiming it, publishing the translation request, and updating the scenario state. `StartingRunners` and `PostProcessing` are handled only through explicit placeholder methods so the lifecycle can be wired without implementing the real runner and post-processing services yet. `StartingRunners` should follow the same claim-before-side-effect shape as translation by first moving the row to the existing in-flight state `InProcessing`, then invoking the runner-start placeholder. `PostProcessing` has no existing separate in-flight state in the current lifecycle, so its BSSL placeholder must remain side-effect-free; the real PostProcessingService integration must add a durable claim design before it performs external side effects.

This change should not introduce advanced scheduling. It should not try to optimize cluster resources, balance load between projects, batch multiple scenarios, or make decisions based on simulation runtime estimates. Those topics are intentionally left for later versions of the scenario selection logic.

The result of this change should be a small, modular selection component that can be called by the Scenario Manager and later replaced or extended without changing the rest of the translation workflow.

## Change Location
The implementation should stay inside the `scenario-manager` component. The AI coding agent should add the new selector as a small Scenario Manager lifecycle component, not as part of EDS ingestion, Translator message formatting, or Experiment Operator logic.

Create a new file `scenario-manager/internal/core/selector.go` for the main selector logic. This file should contain the orchestration code that asks the Core DB for the next actionable scenario and dispatches the correct next step based on the scenario state. The selector should use FIFO behavior based on the `scenario_status.id` column, sorted in ascending order (`1, 2, 3, ...`). For scenarios in `Created`, it should call the existing Translator handoff flow. For scenarios in `StartingRunners`, it should first claim the row by moving it to `InProcessing`, then call a placeholder `createSimulationRunnerJob()` function. For scenarios in `PostProcessing`, it should call a side-effect-free placeholder `doPostProcessing()` function.

Extend `scenario-manager/internal/coredb/scenario_status.go` with the database helper needed by the selector. This helper should return the next actionable scenario candidate from the scenario status table. The returned information should include at least the scenario ID and its current state. The query should only consider states that the selector can act on in this first version: `Created`, `StartingRunners`, and `PostProcessing`. It should sort by `id ASC` and return only the first matching row. This helper should not perform workflow actions itself; it should only read from the database.

Update `scenario-manager/internal/core/scenario_manager.go` so the selector becomes part of Scenario Manager startup. After the existing informer and EDS communication have started, Scenario Manager should initialize the existing Translator communication adapter, start the Translator ready consumer with the existing `HandleTranslatorReady` handler, and then start the selector loop. The startup log message should be adjusted so it no longer says that Scenario Manager is only waiting for a future work loop.

Reuse the existing Translator communication code in `scenario-manager/internal/nats/trans_com.go`. The AI coding agent should not change Translator request subjects, ready subjects, message payloads, or NATS/JetStream semantics as part of this implementation. The selector should use the existing `ProcessScenarioTrans(...)` function for `Created` scenarios instead of introducing a new translation workflow.

No changes are expected in the Experiment Operator, EDS communication logic, Translator communication logic, NATS API types, Kubernetes API types, or external APIs for this step.

## Logic Description

This section describes the exact runtime logic that should be implemented for the first Basic Scenario Selection Logic (BSSL). The implementation must be simple, deterministic, and explicit. It must not introduce resource-aware scheduling, priority sorting, batching, or new communication protocols.

The BSSL owns only the decision loop that finds the next actionable scenario and dispatches the next workflow step. It does not own EDS ingestion, Translator message formatting, Translator ready-message parsing, or real runner/post-processing service implementation.

### Startup Sequence

`RunScenarioManager(ctx)` should start the Scenario Manager dependencies in this order:

1. Start the `SimulationExperiment` informer with the existing `handleSimulationExperimentEvent` handler.
2. Start EDS communication with the existing sequential EDS batch processor.
3. Initialize the existing Translator communication adapter.
4. Start the Translator ready consumer with the existing `HandleTranslatorReady` handler.
5. Start the basic selector loop.

The implementation should use the existing NATS Translator communication implementation. The preferred startup call is `nats.StartTranslatorComms(ctx, HandleTranslatorReady)`, because it initializes the process-scoped Translator adapter and starts the ready-message consumer. The returned adapter must be reused as the `communication.TranslationRequestPublisher` passed into the selector loop.

Future note: when the real PostProcessingService integration is implemented, Scenario Manager startup must also initialize a process-scoped PostProcessingService communication adapter before the selector loop starts. This BSSL step must not implement that adapter yet; `doPostProcessing(...)` remains a local placeholder.

Startup failures for the informer, EDS communication, Translator communication, or selector initialization are fatal. The Scenario Manager should log the failure and terminate, matching the current startup behavior for required dependencies.

After startup succeeds, the Scenario Manager should log that it is ready and that the basic selector loop is running. The old startup log message that says the process is awaiting a future work loop must be removed or replaced.

### Selector Loop

Create the selector implementation in `scenario-manager/internal/core/selector.go`.

The selector should expose a startup function with this behavior:

```go
StartBasicScenarioSelector(ctx context.Context, publisher communication.TranslationRequestPublisher) error
```

`StartBasicScenarioSelector` should:

1. Reject a nil `publisher` with an error.
2. Start one background goroutine for the selector loop.
3. Return nil after the goroutine has been started.
4. Stop the goroutine cleanly when `ctx.Done()` is closed.

The loop should run `processNextScenario(ctx, publisher)` once immediately after startup and then once every `5s`. Each loop iteration processes at most one scenario. If there is no actionable scenario, the iteration is a no-op.

The selector loop has no data-driven shutdown condition in v1. It must not stop merely because all currently stored scenarios are in terminal states. If all scenarios are `Finished` or `Failed`, `NextActionableScenario` returns `nil, nil`, the current tick is a no-op, and the loop waits for the next tick. The loop stops only when `ctx` is cancelled. Future project-level completion logic may detect that all scenarios for a `SimulationExperiment` are terminal, but that must not terminate the process-wide selector loop.

The selector must not terminate the Scenario Manager for ordinary per-iteration failures. Database lookup errors, stale state updates, placeholder failures, and Translator handoff errors should be logged and then the loop should continue on the next tick unless `ctx` has been cancelled.

The selector must not hold a long-lived lock. It selects a candidate row, dispatches the state-specific action, and then returns. Any durable state ownership must be represented by state transitions in the database, not by in-memory locks.

Context cancellation is not a scenario failure. If `ctx.Err()` is non-nil before a state handler starts, after a state handler returns, or while handling an error, the selector must log and return without moving the scenario to `Failed`. This keeps the Core DB as the durable workflow truth across Scenario Manager restarts.

### Actionable Scenario Lookup

Extend `scenario-manager/internal/coredb/scenario_status.go` with a read-only helper:

```go
type ScenarioActionCandidate struct {
	ID    int
	State string
}

func NextActionableScenario(ctx context.Context) (*ScenarioActionCandidate, error)
```

`NextActionableScenario` should:

1. Ensure the Core DB pool is initialized.
2. Query the scenario status table for rows whose `state` is one of:
   - `Created`
   - `StartingRunners`
   - `PostProcessing`
3. Sort candidates by `id ASC`.
4. Return only the first row with `LIMIT 1`.
5. Return `nil, nil` when no actionable row exists.
6. Return an error only for invalid input, missing DB setup, or query failures.

The helper must not update rows. It must not claim scenarios. It must not use `FOR UPDATE`. It is only the selector's read-side candidate lookup.

The selection order is FIFO by the resolved Scenario Status table's `id` column. The default table name is `scenario_status`, but the implementation must continue to respect the existing `SCENARIO_MANAGER_CORE_DB_SCENARIO_STATUS_TABLE` override. The implementation must not use `priority`, `created_at`, `updated_at`, project name, number of repetitions, or any runtime estimate for this first version.

Create one partial index for the selector lookup as part of Core DB schema setup in `scenario-manager/internal/coredb/schema.go`. The index should be created inside `createSchema(ctx)` after the Scenario Status table exists and after the existing Scenario Status schema maintenance has run, before `createSchema(ctx)` returns nil. The index creation SQL must use the resolved table name from `scenarioStatusTableName()` instead of hard-coding `scenario_status`, so deployments and tests that configure a custom Scenario Status table receive the same selector index.

For the default table name, the resulting SQL should be equivalent to:

```sql
CREATE INDEX IF NOT EXISTS scenario_status_actionable_id_idx
ON scenario_status (id)
WHERE state IN ('Created', 'StartingRunners', 'PostProcessing');
```

This index exists because the selector repeatedly asks for the lowest `id` in the resolved Scenario Status table among actionable states. PostgreSQL maintains the index automatically as rows are inserted or their `state` changes. The implementation must not add an application-side background thread or manual index refresh logic.

Do not create separate partial indexes for `Created`, `StartingRunners`, and `PostProcessing` in this BSSL step. The selector performs one unified FIFO lookup across all actionable states, so one partial index containing all actionable rows is the intended v1 optimization.

### Per-Iteration Dispatch

`processNextScenario(ctx, publisher)` should execute this flow:

1. If `ctx` is cancelled, return immediately.
2. Call `coredb.NextActionableScenario(ctx)`.
3. If the result is `nil`, return nil.
4. Switch on `candidate.State`.
5. Dispatch exactly one state handler for that candidate.
6. Return after the state handler finishes.

The selector should handle only these states:

```text
Created
StartingRunners
PostProcessing
```

If another state is returned unexpectedly, log it and do not change the row. This should not happen if `NextActionableScenario` is implemented correctly, but the selector should still be defensive.

### `Created` Handling

When the selected scenario is in `Created`, the selector should call:

```go
ProcessScenarioTrans(ctx, candidate.ID, publisher)
```

The selector must not duplicate Translator handoff logic. It must not directly set `Created -> Scheduled`, increment `translation_attempts`, publish Translator messages, or mark Translator requests as published. All of that remains owned by the existing `ProcessScenarioTrans` workflow and the Core DB helpers it already uses.

Translation already implements the claim-before-side-effect pattern used by BSSL. `ClaimScenarioForTranslation` locks the exact `Created` row, moves it to the existing in-flight state `Scheduled`, increments `translation_attempts`, commits that durable claim, and only then does `ProcessScenarioTrans` publish the Translator request. `Scheduled` is not a temporary implementation-only state; it is the canonical in-flight translation state and the durable ownership marker for Translator work.

If `ProcessScenarioTrans` returns nil, the selector iteration is complete.

If `ProcessScenarioTrans` returns an error, log the scenario ID and the error, then finish the iteration without forcing the scenario to `Failed`. Translator handoff already owns retry and max-attempt behavior:

- publish failure below max attempts returns the row to `Created`
- publish failure at max attempts moves the row to `Failed`
- poison ready response below max attempts returns the row to `Created`
- poison ready response at max attempts moves the row to `Failed`

The selector must not override those existing decisions.

### `StartingRunners` Handling

When the selected scenario is in `StartingRunners`, the selector should first claim runner startup by moving the scenario from `StartingRunners` to `InProcessing`:

```go
MarkScenarioInProcessing(ctx, candidate.ID)
```

If `MarkScenarioInProcessing` returns `false, nil`, the row was stale or already handled by another Scenario Manager instance. The selector should log the stale claim result and return without calling `createSimulationRunnerJob`.

If `ctx` is cancelled after the claim attempt, the selector should return without calling the placeholder and without moving the scenario to `Failed`.

After a successful claim, the selector should call the runner-start placeholder:

```go
createSimulationRunnerJob(ctx, candidate.ID) error
```

The placeholder represents the future creation of the simulation runner Kubernetes Job or equivalent execution mechanism. In this BSSL implementation it should be small, local to the Scenario Manager core package, and explicit that real runner startup is deferred.

For the initial placeholder behavior:

1. Validate that `scenarioID` is positive.
2. Log that runner startup was requested for the scenario.
3. Return nil.

If `createSimulationRunnerJob` returns nil, the selector iteration is complete. The scenario is already in `InProcessing` because that state is the durable runner-start claim and the next canonical workflow state.

If `createSimulationRunnerJob` returns an error and `ctx` has not been cancelled, log the scenario ID and move the scenario to `Failed` with `MarkScenarioFailedFrom(ctx, candidate.ID, "InProcessing")`. If `ctx` has been cancelled, log and return without changing the scenario state.

The selector must not move `InProcessing` to `PostProcessing`. That transition belongs to future runner completion logic. BSSL only claims runner startup, invokes the placeholder, and records that the scenario is now in processing. When real runner startup is implemented, runner creation must remain after the `StartingRunners -> InProcessing` claim and should use an idempotent external identity such as a deterministic Kubernetes Job name based on `scenarioID`.

### `PostProcessing` Handling

When the selected scenario is in `PostProcessing`, the selector should call the post-processing placeholder:

```go
doPostProcessing(ctx, candidate.ID) (confidenceReached bool, err error)
```

The placeholder represents the future call to the post-processing service that evaluates whether enough simulation repetitions have been computed to satisfy the scenario confidence requirement.

In this BSSL implementation, `doPostProcessing` must remain side-effect-free. It may validate input, log the requested post-processing action, and return the placeholder confidence decision, but it must not call a real external PostProcessingService yet. The current canonical lifecycle has no separate existing state equivalent to translation's `Scheduled` or runner startup's `InProcessing` that can serve as a durable post-processing claim. The real PostProcessingService integration must introduce a durable claim design, such as a claim column or a new canonical in-flight state, before making external side-effecting calls.

For the initial placeholder behavior:

1. Validate that `scenarioID` is positive.
2. Log that post-processing was requested for the scenario.
3. Return `true, nil` as the default placeholder result until the real post-processing service exists.

The selector must check `err` before using `confidenceReached`. When `err != nil`, the selector must ignore the returned `confidenceReached` value entirely:

- If `err != nil` and `ctx` has been cancelled, log and return without changing the scenario state.
- If `err != nil` and `ctx` has not been cancelled, log the scenario ID and move the scenario to `Failed` with `MarkScenarioFailedFrom(ctx, candidate.ID, "PostProcessing")`.

Only when `err == nil` should the selector implement both branches of the returned confidence decision:

- If `confidenceReached == true`, update the scenario from `PostProcessing` to `Finished`.
- If `confidenceReached == false`, update the scenario from `PostProcessing` to `StartingRunners`.

The `false` branch models the lifecycle edge where required confidence has not been reached and more simulation runner work is needed. The selector should not immediately start the runner in the same iteration after moving the row back to `StartingRunners`; it should return and let a later selector tick pick up the scenario again.

### Guarded State Updates

Extend `scenario-manager/internal/coredb/scenario_status.go` with guarded state transition helpers:

```go
func MarkScenarioInProcessing(ctx context.Context, scenarioID int) (bool, error)
func MarkScenarioFinished(ctx context.Context, scenarioID int) (bool, error)
func MarkScenarioRequiresMoreRuns(ctx context.Context, scenarioID int) (bool, error)
func MarkScenarioFailedFrom(ctx context.Context, scenarioID int, expectedState string) (bool, error)
```

Each helper should reject non-positive scenario IDs before executing SQL. `MarkScenarioFailedFrom` should also reject an empty `expectedState`, `Finished`, and `Failed`.

`MarkScenarioInProcessing` is the durable claim for runner startup. It should update only the exact row where:

```sql
id = scenarioID
AND state = 'StartingRunners'
```

It should set:

```sql
state = 'InProcessing'
updated_at = NOW()
```

`MarkScenarioFinished` should update only the exact row where:

```sql
id = scenarioID
AND state = 'PostProcessing'
```

It should set:

```sql
state = 'Finished'
updated_at = NOW()
```

`MarkScenarioRequiresMoreRuns` should update only the exact row where:

```sql
id = scenarioID
AND state = 'PostProcessing'
```

It should set:

```sql
state = 'StartingRunners'
updated_at = NOW()
```

`MarkScenarioFailedFrom` should update only the exact row where:

```sql
id = scenarioID
AND state = expectedState
```

It must not change rows already in `Finished` or `Failed`, and it must not change rows that have moved into any other working state after the selector read them.

It should set:

```sql
state = 'Failed'
updated_at = NOW()
```

All four helpers should return `true, nil` when a row was updated and `false, nil` when no row matched the guard. A zero-row update is a stale or already-handled condition, not a hard error. SQL execution failures should return `false, error`.

The selector should log zero-row updates with the scenario ID, expected old state, and intended new state, then continue. It must not retry the same scenario inside the same tick.

### Failure Handling

BSSL uses `Failed` as the terminal failure state for errors that occur in selector-owned workflow steps.

The selector should move a scenario to `Failed` when:

- `createSimulationRunnerJob` returns an error after the selector successfully claimed `StartingRunners -> InProcessing`, and `ctx` has not been cancelled
- `doPostProcessing` returns an error for a `PostProcessing` scenario, and `ctx` has not been cancelled

The selector should not move a scenario to `Failed` when:

- no actionable scenario exists
- `NextActionableScenario` returns a DB error
- `ProcessScenarioTrans` returns an error
- a guarded update returns `false, nil`
- `ctx` is cancelled

Before moving any scenario to `Failed`, the selector must check `ctx.Err()`. If the context is cancelled, cancellation takes precedence over the workflow error and the selector must not write a failure state. If the context is still active, failure updates must use `MarkScenarioFailedFrom` with the state the selector expects to own.

Translator-specific failures remain owned by the existing Translator handoff and ready-message handling logic. In particular, `Scheduled` scenarios must not be selected or failed by BSSL.

Failure reasons are logged only. Do not add a new database column for failure reason, failure class, or failure timestamp in this BSSL step.

### Ignored States

The selector must not select or mutate these states:

- `Scheduled`
- `InProcessing`
- `Finished`
- `Failed`

`Scheduled` is owned by Translator handoff and Translator ready-message handling.

`InProcessing` is entered by BSSL only as the durable runner-start claim and next canonical runner state. The selector must not select `InProcessing` rows. After a scenario is in `InProcessing`, future runner execution and runner completion logic own the next transition.

`Finished` and `Failed` are terminal states.

### Concurrency and Stale Rows

The selector may run in more than one Scenario Manager replica in the future. The v1 implementation should remain correct under stale reads even though it does not implement advanced distributed scheduling.

The design relies on guarded updates and existing exact-row claims:

- `Created` rows are safely claimed by `ProcessScenarioTrans` through the existing `ClaimScenarioForTranslation` transaction.
- `StartingRunners` rows are claimed by moving to `InProcessing` only if the row is still in `StartingRunners`; only the selector instance that successfully claims the row calls the runner-start placeholder.
- `PostProcessing` rows move to `Finished` or `StartingRunners` only if the row is still in `PostProcessing`; the BSSL post-processing placeholder is side-effect-free because no durable post-processing claim exists yet.
- Failure updates require an expected current state, so stale failures do not alter terminal rows or rows that have already moved into a different working state.

If another process changes a row after `NextActionableScenario` reads it, the guarded update should affect zero rows. The selector should log that stale outcome and continue on the next tick.

### Tests and Acceptance Criteria

Add focused tests for the new selector and Core DB helpers.

Core DB helper tests should verify:

- `NextActionableScenario` returns the lowest `id` among `Created`, `StartingRunners`, and `PostProcessing`
- `NextActionableScenario` ignores `Scheduled`, `InProcessing`, `Finished`, and `Failed`
- `NextActionableScenario` returns `nil, nil` when there is no actionable row
- `MarkScenarioInProcessing` only updates `StartingRunners -> InProcessing`
- `MarkScenarioFinished` only updates `PostProcessing -> Finished`
- `MarkScenarioRequiresMoreRuns` only updates `PostProcessing -> StartingRunners`
- `MarkScenarioFailedFrom` updates only rows that still match the expected non-terminal state
- guarded helpers return `false, nil` for stale state guards

Selector tests should use fakes or replaceable function variables for the publisher and placeholders. They should verify:

- a `Created` candidate calls `ProcessScenarioTrans` through the configured publisher path
- a `StartingRunners` candidate is claimed as `InProcessing` before `createSimulationRunnerJob` is called
- successful runner startup leaves the scenario in `InProcessing`
- runner startup failure moves the scenario from `InProcessing` to `Failed`
- a `PostProcessing` candidate calls `doPostProcessing`
- `confidenceReached == true` moves the scenario to `Finished`
- `confidenceReached == false` moves the scenario to `StartingRunners`
- post-processing failure moves the scenario from `PostProcessing` to `Failed`
- DB lookup errors are logged and do not stop the loop permanently
- one loop iteration processes at most one scenario

Startup validation should verify by test or code review that Translator communication is initialized before the selector loop starts and that the selector receives a non-nil `communication.TranslationRequestPublisher`.

The implementation is complete when Scenario Manager can continuously look for the next actionable scenario, dispatch it according to its lifecycle state, reuse the existing Translator handoff for `Created`, claim runner startup before invoking its placeholder, call only a side-effect-free placeholder for post-processing, set successful scenarios to `Finished`, and degrade selector-owned failures to `Failed` only when the row still matches the expected state and the Scenario Manager context has not been cancelled.
