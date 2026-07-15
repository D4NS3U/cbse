# CBSE project status

Reviewed: 2026-07-15

CBSE is an early research prototype for running simulation experiments on Kubernetes. It has the foundations for describing an experiment, preparing its supporting services, receiving scenarios, and asking a Translator to prepare a scenario. It does **not** yet run simulation containers or produce simulation results.

## What the repository contains

- `experiment-operator`: a Kubernetes operator that manages `SimulationExperiment` resources.
- `scenario-manager`: a service that keeps track of experiments and scenarios in PostgreSQL and coordinates messages through NATS JetStream.
- `test-env`: Kubernetes manifests and a mock Experimental Design Service (EDS) for testing scenario intake.

Both Go modules are developed together through the root `go.work` workspace.

## What works today

### Experiment definition and setup

The public `SimulationExperiment` API is version `alpha3`. It describes two databases, a Translator, a post-processing service, and an Experimental Design Service.

The operator creates the Kubernetes resources needed for those components (such as Deployments, Services, Secrets, ConfigMaps, and an EDS Pod). It moves an experiment through `Pending`, `Provisioning`, and `InProgress` once the configured components are ready.

`alpha2` is still served by the CRD only to give existing resources a clear error. The operator does not provision `alpha2` experiments; new resources must use `experiment.cbse.terministic.de/alpha3`.

### Scenario intake and tracking

The Scenario Manager watches `alpha3` experiments and creates, updates, or deletes the matching project record in PostgreSQL.

An EDS can announce that scenarios are available through NATS. Scenario Manager responds with a project-specific address, accepts scenario batches through JetStream, and stores the received scenarios in PostgreSQL. A batch is stored as one transaction, so it is not partly saved.

### Translator handoff and basic selection

Scenario Manager now starts one serial Basic Scenario Selection Logic (BSSL) worker. It checks one scenario at a time, beginning immediately and then every five seconds.

For a newly received scenario, BSSL:

1. Records that the scenario is scheduled for translation.
2. Sends its recipe information to the Translator through JetStream.
3. Records that the message was accepted.
4. Accepts a Translator response containing a container image and moves the scenario to `StartingRunners`.

The handoff uses stored attempt numbers and guarded updates to avoid applying an old response to a newer attempt. If a translation publish is not confirmed, BSSL can retry it or mark it failed after the configured attempt limit.

## What is not implemented

- Creating or running simulation runner Jobs/containers.
- Tracking repetitions, execution progress, results, or experiment completion.
- Calling a real post-processing service or calculating confidence metrics.
- Updating `SimulationExperiment.status.metrics.scenarioCount` from Scenario Manager.
- A completed end-to-end path from an EDS scenario to a finished simulation result.

In particular, the current runner and post-processing steps in BSSL are placeholders. `StartingRunners` is moved to `InProcessing`, but no Kubernetes Job is created. The post-processing placeholder is not connected to a real service.

## Current workflow boundary

```text
SimulationExperiment (alpha3)
        |
        +--> Experiment Operator provisions supporting Kubernetes resources
        |
        +--> Scenario Manager stores the project in PostgreSQL

EDS --> NATS/JetStream --> Scenario Manager --> PostgreSQL scenario records
                                           |
                                           +--> Translator request
                                                     |
Translator ready message <--------------------------+
                                           |
                                           +--> scenario marked StartingRunners

No simulation runner or results path exists yet.
```

## Important current limitations

- The operator provisions services; it does not execute the simulation itself.
- The documented `test-env` EDS test resource still uses `alpha2`. It must be updated to `alpha3` before it can test the current operator/Scenario Manager path.
- The operator API, deployment assets, and tests are still prototype-quality and may change.

## Verification on 2026-07-15

- `go test -count=1 ./experiment-operator/internal/controller ./experiment-operator/cmd` passed.
- The BSSL unit tests and Core DB validation tests passed.
- Database and NATS integration tests were skipped because this environment has no configured PostgreSQL or NATS service.
- The Kubernetes connectivity test failed because this environment is not running inside a Kubernetes cluster.

## Recommended next work

1. Implement a real, idempotent simulation runner and its completion reporting.
2. Connect runner completion to repetition tracking and real post-processing.
3. Update the EDS integration manifest from `alpha2` to `alpha3`, then run it in a Kubernetes cluster.
