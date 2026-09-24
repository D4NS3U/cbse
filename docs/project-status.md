# CBSE project status

Reviewed: 2026-09-23

CBSE is a research prototype for running simulation experiments on Kubernetes. It provisions an experiment's supporting services, receives scenarios from an Experimental Design Service (EDS), translates each scenario into a simulation-runner image, executes the requested repetitions in Kubernetes Jobs, and persists the results in PostgreSQL so the experiment's scenarios can be post-processed.

## What the repository contains

- `experiment-operator`: a Kubernetes operator that manages `SimulationExperiment` resources. The checked-in CRD serves and stores only the `alpha4` API version; `alpha2` and `alpha3` are retired.
- `scenario-manager`: a service that keeps project and scenario state in PostgreSQL, receives EDS scenario batches through NATS JetStream, and coordinates translation, runner execution, and post-processing state through NATS and JetStream.
- `component-templates/translator`: the reference Translator. It consumes translation requests, looks up the scenario's model parameters in the Scenario Detail Database, generates a SimPy-based simulation runner, builds the runner image through a rootless BuildKit sidecar, and pushes it to the registry as an immutable digest-pinned reference.
- `component-templates/scenario-detail-database`: the reference Scenario Detail Database image, holding the `public.simulation_parameters` schema and its four fixed parameter rows.
- `test`: the test harness — shell orchestration in `test/harness/`, Kustomize manifests and Go/Ginkgo smoke assertions in `test/e2e/`, and mock/support components in `test/mocks/`.

The operator and Scenario Manager are Go modules developed together through the root `go.work` workspace, with the reference Translator as a separate module in the same workspace.

## What works today

### Experiment definition and provisioning

The public `SimulationExperiment` API is version `alpha4`, and it is the only version the CRD serves and stores. It requires Kubernetes 1.30 or newer.

The operator moves an experiment through `Pending`, `Provisioning`, and `InProgress` (or `Error` when provisioning validation fails). Along the way it provisions:

- The experiment's Result and Detail database Deployments and Services, plus their connection Secrets.
- The Translator Deployment with its rootless BuildKit sidecar container, the Translator ConfigMap, and the Translator Service.
- The deterministic runner ServiceAccount used by the scenario runner Jobs.

The operator requires the `cbse-registry-auth` registry pull Secret to exist in the experiment namespace, validates it, and uses it as the single pull reference for the database workloads it creates.

### Scenario intake

An EDS announces its scenario batches through NATS; the Scenario Manager answers with a project-specific subject. Batches are published through JetStream and stored in PostgreSQL in one transaction, so a batch is never partly saved.

### Translation to runner images

A selection loop in the Scenario Manager claims scenarios one at a time and publishes durable translation requests to the Translator through JetStream. The reference Translator looks up the scenario's `parameterset_id` in the Scenario Detail Database, generates a SimPy-based runner build context, builds the image through its rootless BuildKit sidecar, and pushes it to the configured runner repository. The ready message carries the pushed image as an immutable digest reference, and the Scenario Manager stores it with the scenario.

### Runner execution with numbered repetitions

For each translated scenario, the Scenario Manager starts one `simrun-*` runner Job. The Job is indexed: its parallelism and completions both equal the scenario's requested `number_of_reps`, so every repetition is one indexed parallel. The runner container runs the digest-pinned generated runner image as a numeric non-root user, and each repetition derives a distinct effective seed.

### Result persistence

Each completed repetition inserts one result row into the scenario's own `scenario_<id>_results` table in the experiment's Result Database. Rows carry the looked-up model parameters, the effective seed, and the computed result fields. The Scenario Manager tracks the number of computed repetitions per scenario.

### Per-experiment PostProcessing

When a scenario's computed repetitions reach its requested count, the Scenario Manager moves it through `InProcessing` to the `PostProcessing` state. That is the current end of the implemented workflow.

## Current workflow boundary

```text
SimulationExperiment (alpha4)
        |
        +--> Experiment Operator provisions Result/Detail DBs, Translator + BuildKit
             sidecar, and runner ServiceAccount; validates the cbse-registry-auth
             pull Secret

EDS --> NATS/JetStream batches --> Scenario Manager --> PostgreSQL scenario records
                                           |
                                           +--> selection loop: translation request (JetStream)
                                                     |
Reference Translator (component-templates/translator)
   Detail DB parameter lookup -> SimPy runner generation
   -> rootless BuildKit sidecar build -> digest-pinned registry push
   -> ready message with the pushed digest
                                           |
                                           +--> simrun-* runner Job (indexed, one parallel
                                              per repetition)
                                                     |
                                          non-root execution, distinct effective seeds
                                                     |
                                          Result DB scenario_<id>_rows
                                                     |
                                           +--> scenario reaches PostProcessing
```

## Verification

- `make test-fast` passes at the current revision (generated verification, formatting, vetting, race suites, envtest, and harness self-tests).
- Green smoke run `20260917130324-e7c707` (2026-09-17): after the one-time `UserNamespacesSupport` enablement, the alpha4 cutover smoke passed 4/4 Ginkgo specs with zero JUnit failures on the dedicated K3s cluster. Provisioning, EDS intake and persistence, idempotent reconciliation, and cleanup all passed; the ephemeral namespace was removed and the smoke Lease released.
- S07-A3 reference end-to-end (2026-09-18, run `s07a3-20260918113143-6ed834`): the suite gained a spec that drives one scenario through the full reference Translator chain — on-demand runner image creation, runner Job execution, and Result DB persistence. It verified real `scenario_<id>_results` rows for every requested repetition with distinct effective seeds and the scenario in `PostProcessing`. The suite passed 5/5 Ginkgo specs with zero JUnit failures.

## Cluster requirements

- Kubernetes 1.30 or newer.
- The `UserNamespacesSupport` feature gate enabled on the cluster — the reference Translator's rootless BuildKit sidecar needs it. See [`CLUSTER_REQUIREMENTS.md`](CLUSTER_REQUIREMENTS.md) for the exact enablement steps, prerequisites, and verification.
- Experiment namespaces must permit the sidecar's unconfined seccomp and AppArmor profiles (Pod Security enforce `privileged`).
- A `cbse-registry-auth` Docker-config Secret in each experiment namespace.

## What is not implemented

- Post-processing beyond the experiment `PostProcessing` state: there is no PostProcessingService API or message contract yet, and the Scenario Manager does not call a post-processing workload.
- Production readiness: the operator, Scenario Manager, and reference components are prototype-quality; the reference Translator and Detail Database are templates, not the only supported integrations.
- A Helm chart or install tooling; `CLUSTER_REQUIREMENTS.md` is written as the prerequisite reference such a chart must use.

## Recommended next work

1. Define and implement the PostProcessingService contract and wire it to scenario completion.
2. Harden the components toward production use and an installable Helm chart built on the documented cluster prerequisites.
