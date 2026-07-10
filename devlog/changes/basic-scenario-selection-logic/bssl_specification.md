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

The selection logic should focus only on states that this first basic Scenario Manager loop can actively move forward: `Created`, `StartingRunners`, and `PostProcessing`. A `Created` scenario has been received and stored, but has not yet been sent to the Translator. Once such a scenario is selected, the existing translator handoff is responsible for claiming it, publishing the translation request, and updating the scenario state. `StartingRunners` and `PostProcessing` are handled only through explicit placeholder methods so the lifecycle can be wired without implementing the real runner and post-processing services yet.

This change should not introduce advanced scheduling. It should not try to optimize cluster resources, balance load between projects, batch multiple scenarios, or make decisions based on simulation runtime estimates. Those topics are intentionally left for later versions of the scenario selection logic.

The result of this change should be a small, modular selection component that can be called by the Scenario Manager and later replaced or extended without changing the rest of the translation workflow.

## Change Location
The implementation should stay inside the `scenario-manager` component. The AI coding agent should add the new selector as a small Scenario Manager lifecycle component, not as part of EDS ingestion, Translator message formatting, or Experiment Operator logic.

Create a new file `scenario-manager/internal/core/selector.go` for the main selector logic. This file should contain the orchestration code that asks the Core DB for the next actionable scenario and dispatches the correct next step based on the scenario state. The selector should use FIFO behavior based on the `scenario_status.id` column, sorted in ascending order (`1, 2, 3, ...`). For scenarios in `Created`, it should call the existing Translator handoff flow. For scenarios in `StartingRunners`, it should call a placeholder `createSimulationRunnerJob()` function. For scenarios in `PostProcessing`, it should call a placeholder `doPostProcessing()` function.

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

The selector must not terminate the Scenario Manager for ordinary per-iteration failures. Database lookup errors, stale state updates, placeholder failures, and Translator handoff errors should be logged and then the loop should continue on the next tick unless `ctx` has been cancelled.

The selector must not hold a long-lived lock. It selects a candidate row, dispatches the state-specific action, and then returns. Any durable state ownership must be represented by state transitions in the database, not by in-memory locks.

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

The selection order is FIFO by `scenario_status.id`. The implementation must not use `priority`, `created_at`, `updated_at`, project name, number of repetitions, or any runtime estimate for this first version.

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

If `ProcessScenarioTrans` returns nil, the selector iteration is complete.

If `ProcessScenarioTrans` returns an error, log the scenario ID and the error, then finish the iteration without forcing the scenario to `Failed`. Translator handoff already owns retry and max-attempt behavior:

- publish failure below max attempts returns the row to `Created`
- publish failure at max attempts moves the row to `Failed`
- poison ready response below max attempts returns the row to `Created`
- poison ready response at max attempts moves the row to `Failed`

The selector must not override those existing decisions.

### `StartingRunners` Handling

When the selected scenario is in `StartingRunners`, the selector should call the runner-start placeholder:

```go
createSimulationRunnerJob(ctx, candidate.ID) error
```

The placeholder represents the future creation of the simulation runner Kubernetes Job or equivalent execution mechanism. In this BSSL implementation it should be small, local to the Scenario Manager core package, and explicit that real runner startup is deferred.

For the initial placeholder behavior:

1. Validate that `scenarioID` is positive.
2. Log that runner startup was requested for the scenario.
3. Return nil.

If `createSimulationRunnerJob` returns nil, update the scenario from `StartingRunners` to `InProcessing`.

If `createSimulationRunnerJob` returns an error, log the scenario ID and move the scenario to `Failed`.

The selector must not move `InProcessing` to `PostProcessing`. That transition belongs to future runner completion logic. BSSL only starts the runner and records that the scenario is now in processing.

### `PostProcessing` Handling

When the selected scenario is in `PostProcessing`, the selector should call the post-processing placeholder:

```go
doPostProcessing(ctx, candidate.ID) (confidenceReached bool, err error)
```

The placeholder represents the future call to the post-processing service that evaluates whether enough simulation repetitions have been computed to satisfy the scenario confidence requirement.

For the initial placeholder behavior:

1. Validate that `scenarioID` is positive.
2. Log that post-processing was requested for the scenario.
3. Return `true, nil` as the default placeholder result until the real post-processing service exists.

The selector must still implement both branches of the returned confidence decision:

- If `confidenceReached == true`, update the scenario from `PostProcessing` to `Finished`.
- If `confidenceReached == false`, update the scenario from `PostProcessing` to `StartingRunners`.
- If `err != nil`, log the scenario ID and move the scenario to `Failed`.

The `false` branch models the lifecycle edge where required confidence has not been reached and more simulation runner work is needed. The selector should not immediately start the runner in the same iteration after moving the row back to `StartingRunners`; it should return and let a later selector tick pick up the scenario again.

### Guarded State Updates

Extend `scenario-manager/internal/coredb/scenario_status.go` with guarded state transition helpers:

```go
func MarkScenarioInProcessing(ctx context.Context, scenarioID int) (bool, error)
func MarkScenarioFinished(ctx context.Context, scenarioID int) (bool, error)
func MarkScenarioRequiresMoreRuns(ctx context.Context, scenarioID int) (bool, error)
func MarkScenarioFailed(ctx context.Context, scenarioID int) (bool, error)
```

Each helper should reject non-positive scenario IDs before executing SQL.

`MarkScenarioInProcessing` should update only the exact row where:

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

`MarkScenarioFailed` should update only non-terminal rows. It must not change rows already in `Finished` or `Failed`.

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

- `createSimulationRunnerJob` returns an error for a `StartingRunners` scenario
- `doPostProcessing` returns an error for a `PostProcessing` scenario

The selector should not move a scenario to `Failed` when:

- no actionable scenario exists
- `NextActionableScenario` returns a DB error
- `ProcessScenarioTrans` returns an error
- a guarded update returns `false, nil`
- `ctx` is cancelled

Translator-specific failures remain owned by the existing Translator handoff and ready-message handling logic. In particular, `Scheduled` scenarios must not be selected or failed by BSSL.

Failure reasons are logged only. Do not add a new database column for failure reason, failure class, or failure timestamp in this BSSL step.

### Ignored States

The selector must not select or mutate these states:

- `Scheduled`
- `InProcessing`
- `Finished`
- `Failed`

`Scheduled` is owned by Translator handoff and Translator ready-message handling.

`InProcessing` is owned by future runner execution and runner completion logic.

`Finished` and `Failed` are terminal states.

### Concurrency and Stale Rows

The selector may run in more than one Scenario Manager replica in the future. The v1 implementation should remain correct under stale reads even though it does not implement advanced distributed scheduling.

The design relies on guarded updates and existing exact-row claims:

- `Created` rows are safely claimed by `ProcessScenarioTrans` through the existing `ClaimScenarioForTranslation` transaction.
- `StartingRunners` rows move to `InProcessing` only if the row is still in `StartingRunners`.
- `PostProcessing` rows move to `Finished` or `StartingRunners` only if the row is still in `PostProcessing`.
- Failure updates do not alter terminal rows.

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
- `MarkScenarioFailed` updates non-terminal rows but leaves `Finished` and `Failed` unchanged
- guarded helpers return `false, nil` for stale state guards

Selector tests should use fakes or replaceable function variables for the publisher and placeholders. They should verify:

- a `Created` candidate calls `ProcessScenarioTrans` through the configured publisher path
- a `StartingRunners` candidate calls `createSimulationRunnerJob`
- successful runner startup moves the scenario to `InProcessing`
- runner startup failure moves the scenario to `Failed`
- a `PostProcessing` candidate calls `doPostProcessing`
- `confidenceReached == true` moves the scenario to `Finished`
- `confidenceReached == false` moves the scenario to `StartingRunners`
- post-processing failure moves the scenario to `Failed`
- DB lookup errors are logged and do not stop the loop permanently
- one loop iteration processes at most one scenario

Startup validation should verify by test or code review that Translator communication is initialized before the selector loop starts and that the selector receives a non-nil `communication.TranslationRequestPublisher`.

The implementation is complete when Scenario Manager can continuously look for the next actionable scenario, dispatch it according to its lifecycle state, reuse the existing Translator handoff for `Created`, call placeholders for runner startup and post-processing, set successful scenarios to `Finished`, and degrade selector-owned failures to `Failed` without changing code outside the documented Scenario Manager and Core DB boundaries.
