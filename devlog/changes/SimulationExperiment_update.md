# SimulationExperiment alpha3 Implementation Plan

## Goal

Introduce `alpha3` as the next API version of `SimulationExperiment` and make the CRD-driven project identity available to every operator-managed runtime component through a shared environment variable.

## Core Change

`SimulationExperiment.metadata.name` becomes the canonical project token for the deployment, and the `experiment-operator` injects that value into each operator-managed workload as an environment variable named `SIMULATIONPROJECTNAME`.

Target components:
- `detailDatabase`
- `resultDatabase`
- `translator`
- `postProcessingService`
- `experimentalDesignService`

Out of scope:
- `defaultServiceType`
- `Service` objects and any other non-workload resources
- host-based database mode, because it does not create Kubernetes workload objects
- `scenario-manager` and every path outside `cbse/experiment-operator`

## Baseline Architecture

- `alpha3` replaces `alpha2` as the active API version for `SimulationExperiment`
- the `alpha3` spec should preserve the current `alpha2` shape unless a field must change to support the env injection workflow
- `scenario-manager` dependencies on `alpha2` are not updated as part of this change and will be handled later
- the `experiment-operator` derives the project token from `SimulationExperiment.metadata.name`
- every operator-managed `Deployment`, `Pod`, and `StatefulSet` created for a `SimulationExperiment` receives `SIMULATIONPROJECTNAME` at startup
- components should not need to query Kubernetes at runtime to discover the project token
- all changes must stay inside `cbse/experiment-operator` and follow kubebuilder-supported patterns

## Why This Change

- the project token is routing configuration, not mutable experiment state
- env injection keeps startup deterministic and makes local testing easier
- avoiding runtime discovery removes an unnecessary dependency on Kubernetes API access and RBAC
- all components can construct the same NATS subjects from the same stable token
- a single generic env var name across workloads keeps component integration consistent

## Expected Result

- every operator-managed workload created by `experiment-operator` for a `SimulationExperiment` receives `SIMULATIONPROJECTNAME=<SimulationExperiment.metadata.name>`
- `alpha3` CRDs, generated code, and sample manifests are regenerated so the new API version is usable
- `alpha2` implementation paths inside `experiment-operator` are replaced by `alpha3` equivalents where required for the operator to build and reconcile correctly
- no files outside `cbse/experiment-operator` are changed

## Implementation Constraints

- implement the change only in `cbse/experiment-operator`
- use kubebuilder-supported API versioning, generated code, and CRD regeneration workflows
- update controller logic only for workload-producing paths
- inject `SIMULATIONPROJECTNAME` into container env definitions for every operator-managed workload produced by the reconciler
- do not add env handling to `Service`, `ConfigMap`, `Secret`, or other non-workload resources unless needed only as supporting infrastructure for workload reconciliation
- do not change `scenario-manager`, Translator runtime logic outside the operator repo, or any other sibling project

## Acceptance Criteria

- `experiment-operator/api/alpha3` exists and replaces `alpha2` for operator-owned code paths
- the controller reconciles all supported `SimulationExperiment` workload components using `alpha3`
- every operator-created workload pod template includes `SIMULATIONPROJECTNAME`
- host-based database handling remains unchanged
- generated deepcopy code and CRDs are updated
- sample manifests under `experiment-operator/config/samples` use `alpha3`

## Translator Spec Guidance

The Translator communication spec should assume the Translator already receives its project token through `SIMULATIONPROJECTNAME`. It should not describe discovery mechanics beyond noting that the value originates from the `SimulationExperiment.metadata.name` deployment setup performed by `experiment-operator`.
