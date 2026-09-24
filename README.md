# Container-Based Simulation Environment (CBSE)

The **Container-Based Simulation Environment (CBSE)** explores the integration of large-scale simulation workflows with **Kubernetes-native infrastructure**.

CBSE is a research-driven framework investigating how container orchestration can serve as computational infrastructure for simulation experiments.

---

## Project Status

⚠️ **Research Prototype – Early Development Stage**

CBSE runs a full simulation experiment on Kubernetes: an Experiment Operator provisions the experiment's supporting services, the Scenario Manager receives scenarios from an Experimental Design Service (EDS) and selects them for translation, a Translator generates and builds the simulation-runner image, `simrun-*` runner Jobs execute the requested repetitions, and results persist in PostgreSQL for post-processing.

The current public `SimulationExperiment` API is `alpha4`, the only version the CRD serves and stores; new experiments must use this version, and it targets Kubernetes 1.30 or newer. The codebase and its interfaces are still evolving.

The implemented and smoke-verified chain:

- **Experiment Operator (ExOp)**
  - Provides the `alpha4` API for the `SimulationExperiment` Custom Resource Definition (CRD)
  - Provisions the experiment's Result and Detail database workloads and connection Secrets, the Translator Deployment with its rootless BuildKit sidecar, and the runner ServiceAccount
  - Validates the `cbse-registry-auth` registry pull Secret in the experiment namespace
- **Scenario Manager**
  - Receives scenario batches from an EDS through NATS/JetStream and persists them in PostgreSQL
  - Runs the scenario selection loop and starts the `simrun-*` runner Jobs
- **Reference Translator** (`component-templates/translator`)
  - Generates a SimPy-based simulation runner and builds it through the rootless BuildKit sidecar
  - Pushes the generated runner image to the registry as an immutable digest-pinned reference
- **Simulation runners**
  - `simrun-*` Jobs run the digest-pinned runner image non-root, one indexed repetition per requested repetition, each with a distinct seed
  - Result rows persist in the experiment's per-scenario PostgreSQL Result DB tables
- **PostProcessing**
  - A scenario reaches the `PostProcessing` state once all of its requested repetitions are computed

Not yet implemented:

- A post-processing service contract beyond the experiment `PostProcessing` state
- Production-ready feature set

Interfaces and behavior may change without notice.
For the full, plain-language overview of what is implemented, what is still missing, and the latest verification results, see [Project status](docs/project-status.md).

## Start here

Want to supply your own Experimental Design Service, Translator, or PostProcessingService? Start with [Designing Custom CBSE Components](docs/COMPONENT_DESIGN_GOALS.md). It explains the current architecture and scenario lifecycle, the implemented EDS and Translator message contracts, the not-yet-implemented post-processing boundary, container design goals, and a practical implementation checklist.

The most useful follow-up references are:

- [Project status](docs/project-status.md) for the exact implemented product boundary.
- [Testing guide](docs/CBSE_TESTING_GUIDE.md) for repository tests, smoke architecture, and diagnostics.
- [Experiment Operator README](experiment-operator/README.md) for operator development and generated API assets.

---

## Publications

Related research and conceptual foundations:

| Publication | Conference | Date |
|-------------|------------|------|
| *Towards Container-Based Simulation: A Concept For A Distributed And Scalable Simulation Framework* | 12th Simulation Workshop | April 2025 |
| *Container-Based Simulation: A Concept For Large-Scale Simulation Environments* | 27. ASIM Symposium Simulationstechnik | January 2024 |
| *Using Kubernetes to Improve Data Farming Capabilities* | 2023 Winter Simulation Conference | December 2023 |
| *On the Usage of Containers and Container Orchestrators as a Computational Infrastructure for Simulation Experiments* | 20. ASIM Fachtagung Produktion und Logistik | September 2023 |

More: [ResearchGate Profile](https://www.researchgate.net/profile/Daniel-Seufferth/research)

---

## License

Licensed under the **Apache License 2.0**. See `LICENSE`.

## Testing

The repository exposes one test contract for developers, coding agents, and CI:

```bash
make test-fast
```

The production-like smoke suite uses the dedicated K3s cluster and freshly published `linux/amd64` images:

```bash
make test-smoke \
  KUBECONFIG=/home/d4ns3u/.kube/config \
  TEST_IMAGE_VERSION=26.7.16 \
  CBSE_REGISTRY_AUTH_FILE=<protected-docker-config>
```

The default Harbor repository prefix is `registry.unibw.de/i31bdase/cbse-test`; the smoke build publishes the Makefile default six-component image set from `CBSE_IMAGE_COMPONENTS` (`exop`, `sm`, `eds-mock`, `translator`, `runner-base`, `scenario-detail-database`) beneath it, and generated runner images are published to a separate `cbse-test-runner` repository. The cluster must have the `UserNamespacesSupport` feature gate enabled for the reference Translator's rootless BuildKit sidecar — see [`docs/CLUSTER_REQUIREMENTS.md`](docs/CLUSTER_REQUIREMENTS.md). Supply a dedicated Docker configuration through `CBSE_REGISTRY_AUTH_FILE`; never commit it. Each run receives an isolated namespace, is serialized with a Kubernetes Lease, writes diagnostics to `artifacts/test/<run-id>/`, and cleans itself up. See [`docs/CBSE_TESTING_GUIDE.md`](docs/CBSE_TESTING_GUIDE.md) for the current architecture, test layout, and artifact-reading guide.
