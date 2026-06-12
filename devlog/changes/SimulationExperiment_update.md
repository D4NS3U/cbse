# SimulationExperiment alpha3 Baseline Plan

## Goal

Introduce `alpha3` as the next API version of `SimulationExperiment` and make the CRD-driven project identity available to every runtime component through environment variables.

## Core Change

`SimulationExperiment.metadata.name` becomes the canonical project token for the deployment, and the operator or deployment layer injects that value into each component as an environment variable.

Target components:
- `detailDatabase`
- `resultDatabase`
- `translator`
- `postProcessingService`
- `experimentalDesignService`
- `defaultServiceType`

## Baseline Architecture

- `alpha3` should preserve the current `SimulationExperiment` shape unless a field must change to support the env injection workflow
- the operator or deployment layer derives the project token from `SimulationExperiment.metadata.name`
- each deployed component receives that token through env/config at startup
- components should not need to query Kubernetes at runtime to discover the project token

## Why This Change

- the project token is routing configuration, not mutable experiment state
- env injection keeps startup deterministic and makes local testing easier
- avoiding runtime discovery removes an unnecessary dependency on Kubernetes API access and RBAC
- all components can construct the same NATS subjects from the same stable token

## Expected Result

- EDS publishes to `cbse.{project}.eds.scenarios`
- Translator subscribes to `cbse.{project}.trans.request`
- Scenario Manager continues resolving the project through the `SimulationExperiment` informer and Core DB persistence path
- every component reads the project token from env/config instead of inferring it dynamically

## Translator Spec Guidance

The Translator communication spec should assume the Translator already receives its project token through config. It should not describe discovery mechanics beyond noting that the value originates from the `SimulationExperiment.metadata.name` deployment setup.
