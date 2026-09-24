# Container-Based Simulation Environment (CBSE)

<!--
CI badge — reflects the repository's public CI workflow. The badge points at
the public verify.yml workflow on github.com/D4NS3U/cbse; no private runners
are referenced.
-->
![Verify](https://github.com/D4NS3U/cbse/actions/workflows/verify.yml/badge.svg)

The **Container-Based Simulation Environment (CBSE)** is a Kubernetes-native framework for running simulation experiments. An Experiment Operator provisions an experiment's supporting services, a Scenario Manager receives scenarios from an Experimental Design Service (EDS) and coordinates their translation and execution, a Translator generates and builds the simulation-runner image, Kubernetes Jobs execute the requested repetitions, and results persist in PostgreSQL for post-processing.

CBSE is a research-driven framework investigating how container orchestration can serve as computational infrastructure for large-scale simulation experiments.

## Project Status

⚠️ **Research Prototype – Early Development Stage**

The current public `SimulationExperiment` API is `alpha4`, the only version the CRD serves and stores; new experiments must use this version, and it targets Kubernetes 1.30 or newer. The codebase and its interfaces are still evolving.

## Architecture

CBSE runs a full simulation experiment on Kubernetes. The implemented and smoke-verified chain:

- **Experiment Operator** (`experiment-operator/`)
  - Provides the `alpha4` API for the `SimulationExperiment` Custom Resource Definition (CRD)
  - Provisions the experiment's Result and Detail database workloads and connection Secrets, the Translator Deployment with its rootless BuildKit sidecar, and the runner ServiceAccount
  - Validates the `cbse-registry-auth` registry pull Secret in the experiment namespace
- **Scenario Manager** (`scenario-manager/`)
  - Receives scenario batches from an EDS through NATS/JetStream and persists them in PostgreSQL
  - Runs the scenario selection loop and starts the `simrun-*` runner Jobs
- **Reference Translator** (`component-templates/translator/`)
  - Consumes translation requests, looks up the scenario's model parameters in the Scenario Detail Database, generates a SimPy-based simulation runner, and builds it through the rootless BuildKit sidecar
  - Pushes the generated runner image to the registry as an immutable digest-pinned reference
- **Reference Scenario Detail Database** (`component-templates/scenario-detail-database/`)
  - A container image providing the `public.simulation_parameters` schema and its fixed parameter rows, which the Translator queries
- **Simulation runners**
  - `simrun-*` Jobs run the digest-pinned runner image non-root, one indexed repetition per requested repetition, each with a distinct seed
  - Result rows persist in the experiment's per-scenario PostgreSQL Result DB tables
- **PostProcessing**
  - A scenario reaches the `PostProcessing` state once all of its requested repetitions are computed
- **Test harness** (`test/`)
  - Shell orchestration in `test/harness/`, Kustomize manifests and Go/Ginkgo smoke assertions in `test/e2e/`, and mock/support components in `test/mocks/`

Not yet implemented:

- A post-processing service contract beyond the experiment `PostProcessing` state
- Production-ready feature set

Interfaces and behavior may change without notice.

### Supplying your own components

Want to supply your own Experimental Design Service, Translator, or PostProcessingService? Start with [Designing Custom CBSE Components](docs/COMPONENT_DESIGN_GOALS.md). It explains the current architecture and scenario lifecycle, the implemented EDS and Translator message contracts, the not-yet-implemented post-processing boundary, container design goals, and a practical implementation checklist.

The most useful follow-up references are:

- [Testing guide](docs/CBSE_TESTING_GUIDE.md) for repository tests, smoke architecture, and diagnostics.
- [Cluster requirements](docs/CLUSTER_REQUIREMENTS.md) for the runtime cluster requirements, including the `UserNamespacesSupport` feature gate.
- [Experiment Operator README](experiment-operator/README.md) for operator development and generated API assets.
- [Test harness README](test/README.md) for the test harness layout.

## Quickstart (from source)

**Today's path is source.** Installation by Helm chart is a planned future capability — a designated forward reference for Package B (release pipeline) of the repository professionalization program — and no chart exists yet. Until then, build and run CBSE from this repository.

### Prerequisites

- A Go toolchain of the 1.26 era — the workspace (`go.work`) pins `go 1.26.3`
- `make`

### Steps

```bash
# D4NS3U is the public GitHub organization hosting the repository
git clone https://github.com/D4NS3U/cbse.git
cd cbse

# List all test commands
make help

# Hermetic verification: unit tests, vet, format check, and generated-artifact verification
make test-fast
```

Individual modules can be developed with plain Go commands inside the workspace:

```bash
cd scenario-manager && go test ./...
cd experiment-operator && go test ./...
cd component-templates/translator && go test ./...
```

## Testing

The repository exposes one test contract for developers, coding agents, and CI:

```bash
make test-fast
```

`make test-fast` is publicly runnable and hermetic — it needs no cluster, registry, or credentials. It runs the fast test tiers, a harness self-test, and verifies that generated artifacts (CRD, RBAC role, DeepCopy code) are in sync with the API definitions. Continuous integration runs it on every push and pull request through the public [`verify.yml`](.github/workflows/verify.yml) workflow.

A production-like smoke suite is available as well. It is **environment-gated**: it requires a user-provided Kubernetes cluster and container registry, supplied through environment variables. **No repository default exists for any of them** — every value is yours to provide:

```bash
make test-smoke \
  KUBECONFIG=<your-kubeconfig-path> \
  TEST_IMAGE_VERSION=<image-version> \
  CBSE_REGISTRY=<your-registry> \
  CBSE_REGISTRY_AUTH_FILE=<path-to-docker-config>
```

- `KUBECONFIG` — kubeconfig of a cluster that meets the [cluster requirements](docs/CLUSTER_REQUIREMENTS.md) (Kubernetes >= 1.30, `UserNamespacesSupport` feature gate enabled for the reference Translator's rootless BuildKit sidecar).
- `TEST_IMAGE_VERSION` — version label for the published test image set (defaults to the current UTC date when omitted).
- `CBSE_REGISTRY` — the container registry (repository prefix) where the test image set is published; environment-provided, never embedded in the repository.
- `CBSE_REGISTRY_AUTH_FILE` — path to a Docker configuration file with pull/push rights for `CBSE_REGISTRY`; never commit it.

Each run receives an isolated namespace, is serialized with a Kubernetes Lease, writes diagnostics to `artifacts/test/<run-id>/`, and cleans itself up. See [Testing guide](docs/CBSE_TESTING_GUIDE.md) for the current architecture, test layout, and artifact-reading guide.

## Publications

Related research and conceptual foundations:

| Publication | Conference | Date |
|-------------|------------|------|
| *Towards Container-Based Simulation: A Concept For A Distributed And Scalable Simulation Framework* | 12th Simulation Workshop | April 2025 |
| *Container-Based Simulation: A Concept For Large-Scale Simulation Environments* | 27. ASIM Symposium Simulationstechnik | January 2024 |
| *Using Kubernetes to Improve Data Farming Capabilities* | 2023 Winter Simulation Conference | December 2023 |
| *On the Usage of Containers and Container Orchestrators as a Computational Infrastructure for Simulation Experiments* | 20. ASIM Fachtagung Produktion und Logistik | September 2023 |

More: [ResearchGate Profile](https://www.researchgate.net/profile/Daniel-Seufferth/research)

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md) for participation rules, the test contract, development setup, and the pull-request flow. Security reports go through [SECURITY.md](SECURITY.md).

## License

Licensed under the **Apache License 2.0**. See `LICENSE`.
