# SimulationExperiment Alpha3 Support in Scenario Manager

## Summary

Update the existing `scenario-manager` codebase so it supports `SimulationExperiment` resources in API version `experiment.cbse.terministic.de/alpha3`.

This is an API-version cutover, not a schema redesign. The `alpha3` `SimulationExperiment` shape is effectively the same as `alpha2` for the fields currently consumed by `scenario-manager`, so the implementation work is primarily replacing versioned imports and types, updating Kubernetes client and informer wiring, and aligning tests and documentation with `alpha3`.

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

## Test Plan

- Update Kubernetes integration coverage so a successful client setup and list operation validates `alpha3` scheme registration.
- Add or update informer-oriented tests so add, update, and delete processing works with `alpha3` objects.
- Verify phase transitions still emit the same event types for the same status changes.
- Verify project persistence still behaves the same for `alpha3` resources:
  add creates a project row;
  updates change status and component count when applicable;
  delete removes the project row.
- Run `go test ./...` in `scenario-manager`.
- Verify no `alpha2` `SimulationExperiment` imports remain in `scenario-manager` after implementation.

## Assumptions

- `scenario-manager` moves to `alpha3` only in this change.
- Existing `alpha2` resources in a cluster are out of scope for this implementation.
- The `alpha3` schema preserves the `alpha2` fields currently used by `scenario-manager`.
- Operator-side runtime env-var injection work is related system context, but it is not a primary implementation requirement for this `scenario-manager` cutover.

## Must-Edit Components

- `devlog/changes/update_to_alpha3_api/update_to_alpha3_api_implspe.md`
- `scenario-manager/internal/kube/kubeconnect.go`
- `scenario-manager/internal/kube/simulationexperiments.go`
- `scenario-manager/internal/core/simexp_informer.go`
- `scenario-manager/internal/kube/kubeconnect_test.go`

## Likely Additional Test Component

- Add a dedicated informer test file next to `scenario-manager/internal/core/simexp_informer.go` if current coverage is not enough to validate `alpha3` event handling end to end.
