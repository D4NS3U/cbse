# SimulationExperiment Alpha3 Support in Scenario Manager

## Summary

Update the existing `scenario-manager` codebase so it supports `SimulationExperiment` resources in API version `experiment.cbse.terministic.de/alpha3`.

This is an API-version cutover, not a schema redesign. The `alpha3` `SimulationExperiment` shape is effectively the same as `alpha2` for the fields currently consumed by `scenario-manager`, so the implementation work is primarily replacing versioned imports and types, updating Kubernetes client and informer wiring, and aligning documentation and lightweight verification with `alpha3`.

The target outcome is that every `scenario-manager` path that reads, lists, watches, or persists `SimulationExperiment` data operates against `alpha3` resources only.

## Implementation Changes

### Scope and behavior

- Treat `alpha3` as the active and only supported `SimulationExperiment` version for `scenario-manager`.
- Preserve existing runtime behavior:
  create, update, and delete observation stays the same;
  phase-driven event emission stays the same;
  persistence into the core DB stays the same.
- Do not introduce field-mapping or conversion logic between `alpha2` and `alpha3`.
- Do not add dual-version watch support in this change.
- Do not add new test coverage or refactor testability as part of this change.
- Do not fix the existing Kubernetes integration test behavior unless it blocks compilation of the alpha3 cutover.

### Kubernetes client and listing

- Update Kubernetes scheme registration so `scenario-manager` registers `github.com/D4NS3U/cbse/experiment-operator/api/alpha3` instead of `api/alpha2`.
- Update the list helper so it uses `alpha3.SimulationExperimentList` and returns `[]alpha3.SimulationExperiment`.
- Remove remaining `alpha2` imports from `scenario-manager` code paths that interact with `SimulationExperiment` resources.

### Informer and event model

- Update the informer setup to watch `alpha3.SimulationExperiment` objects.
- Change `SimulationExperimentEvent.Experiment` to `*alpha3.SimulationExperiment`.
- Update all informer handler type assertions from `alpha2` to `alpha3`.
- Update helper function signatures that accept `SimulationExperimentSpec`, `DatabaseSpec`, `TranslatorSpec`, `PostProcessingSpec`, or `ExperimentalDesignServiceSpec` so they use `alpha3` types.
- Keep event semantics unchanged:
  emit events for create and delete;
  emit phase-transition events for `InProgress`, `Completed`, `Failed`, and `Error`;
  ignore updates that do not change phase.

### Persistence and assumptions

- Keep project persistence rules unchanged:
  `project_name` continues to come from `SimulationExperiment.metadata.name`;
  `status` continues to mirror `.status.phase`;
  `number_of_components` continues to be derived from which component specs are defined.
- Assume `experiment-operator/api/alpha3` remains the shared source of truth for the Go types consumed by `scenario-manager`.
- Assume the cluster CRD served by the operator includes `alpha3` and that `scenario-manager` runs against that CRD version.

## Public Interfaces Affected

- `ListSimulationExperiments(...)` changes return type from `[]alpha2.SimulationExperiment` to `[]alpha3.SimulationExperiment`.
- `SimulationExperimentEvent.Experiment` changes from `*alpha2.SimulationExperiment` to `*alpha3.SimulationExperiment`.
- Internal informer helper functions and component-count helpers change from `alpha2` type parameters to `alpha3` type parameters.
- No CR spec field additions or removals are required for `scenario-manager` in this change.

## Verification Plan

Tests are intentionally out of scope for this cutover. Do not add new informer tests, do not update Kubernetes integration tests, and do not require `go test ./...` as the acceptance command for this change.

Required verification:

- Run Go tests for packages that do not require in-cluster Kubernetes access.
- Run `go test ./internal/kube` only in a cluster-capable environment.
- Verify no migration-relevant `alpha2` imports remain in `scenario-manager` after implementation:
  `rg "api/alpha2|experimentalpha2" scenario-manager`
- Confirm the code compiles after changing all `SimulationExperiment` client, list, informer, event, and helper types to `alpha3`.

## Assumptions

- `scenario-manager` moves to `alpha3` only in this change.
- Existing `alpha2` resources in a cluster are out of scope for this implementation.
- The `alpha3` schema preserves the `alpha2` fields currently used by `scenario-manager`.
- New or expanded automated test coverage is out of scope for this implementation.
- Operator-side runtime env-var injection work is related system context, but it is not a primary implementation requirement for this `scenario-manager` cutover.

## Must-Edit Components

- `scenario-manager/internal/kube/kubeconnect.go`
- `scenario-manager/internal/kube/simulationexperiments.go`
- `scenario-manager/internal/core/simexp_informer.go`

## Deferred Work

- Add informer-oriented tests for create, update, delete, and phase-transition event handling with `alpha3` objects.
- Add or update persistence tests for project creation, status/component-count updates, and delete behavior with `alpha3` resources.
- Clean up `KubeConnect` testability so local `go test ./...` can skip Kubernetes integration cleanly when no in-cluster config is available.

# Integrated Changes Successfully to main
