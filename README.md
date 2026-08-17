# Container-Based Simulation Environment (CBSE)

The **Container-Based Simulation Environment (CBSE)** explores the integration of large-scale simulation workflows with **Kubernetes-native infrastructure**.

CBSE is a research-driven framework investigating how container orchestration can serve as computational infrastructure for simulation experiments.

---

## Project Status

⚠️ **Research Prototype – Early Development Stage**

CBSE can define and provision an experiment's supporting Kubernetes services, receive scenarios from an Experimental Design Service, and send scenarios to a Translator. It does **not** yet run simulation containers, manage repetitions, or produce results.

The current public `SimulationExperiment` API is `alpha3`; new experiments must use this version. The codebase and its interfaces are still evolving.

- **Experiment Operator (ExOp)**
  - Provides a tested `alpha3` API for the `SimulationExperiment` Custom Resource Definition (CRD)
  - Supports creation and lifecycle handling of CR instances

Not yet implemented:

- Functional experiment execution
- Scenario orchestration
- Distributed replication management
- Production-ready feature set

Interfaces and behavior may change without notice.
For the full, plain-language overview of what is implemented, what is still missing, and the latest verification results, see [Project status](docs/project-status.md).

## Start here

Want to supply your own Experimental Design Service, Translator, or PostProcessingService? Start with [Designing Custom CBSE Components](COMPONENT_DESIGN_GOALS.md). It explains the current architecture and scenario lifecycle, the implemented EDS and Translator message contracts, the not-yet-implemented post-processing boundary, container design goals, and a practical implementation checklist.

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
  CBSE_REGISTRY_AUTH_FILE=/secure/dockerhub-config.json
```

The default repository is the private Docker Hub repository `docker.io/d4ns3u/cbse-testing`. Create the dedicated config with a Docker Hub personal access token; never commit it. Each run receives an isolated namespace, is serialized with a Kubernetes Lease, writes diagnostics to `artifacts/test/<run-id>/`, and cleans itself up. See [`docs/CBSE_TESTING_GUIDE.md`](docs/CBSE_TESTING_GUIDE.md) for the current architecture, test layout, and artifact-reading guide.
