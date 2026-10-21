# Container-Based Simulation Environment (CBSE)

CBSE is a Kubernetes-native framework for running simulation experiments. An **Experiment Operator** provisions each experiment's supporting services from a `SimulationExperiment` Custom Resource; a **Scenario Manager** receives scenario designs from an **Experimental Design Service (EDS)** over NATS/JetStream and coordinates their translation and execution; a **Translator** generates and builds the simulation-runner image; Kubernetes Jobs execute the requested repetitions; and results persist in PostgreSQL for statistical post-processing. It is a research-driven framework investigating how container orchestration can serve as computational infrastructure for large-scale simulation experiments.

> Status: research prototype, early development. The public `SimulationExperiment` API is **alpha4** (the only version the CRD serves and stores). Generated at merged `main` commit `c993397`.

## Key Concepts

- **`SimulationExperiment` CR (alpha4)** — the control-plane interface: spec in (name, image, settings), status/verdict out (phase, message, `scenarioManagerVerdict`).
- **Experiment Operator** — reconciles one CR into the per-experiment component set (databases, Translator, PPS, EDS) plus the stack-level shared core (Scenario Manager, NATS, core DB).
- **Scenario Manager (SM)** — the experiment namespace's core: NATS batch intake, the PostgreSQL-backed scenario state machine, runner Job orchestration, phase aggregation, and the finalization-aware verdict.
- **Experimental Design Service (EDS)** — the reference design surface: a REST API + embedded UI over the Scenario Detail Database, publishing scenario batches and the experiment Finish handshake over the SM-By-Omission (SBO) wire.
- **Translator** — turns a scenario's recipe into generated Python runner code and a built runner image (rootless BuildKit sidecar).
- **Simulation Runner** — the *generated* component (from `runnermod` templates): a Kubernetes Job that reads the Detail DB, simulates, and writes result rows.
- **Post Processing Service (PPS)** — the statistical gate: evaluates each scenario's results against the target CI half-width and requests top-up rounds.
- **Scenario Detail DB / Result DB** — the experiment-scoped PostgreSQL pair: the six-object design schema and the per-scenario result tables.
- **Finalization** — an explicit, irrevocable Finish: a durable `experiment_finalization` marker plus all-terminal scenarios ⇒ the experiment verdict `Finished`.

## Entry Points

- [`experiment-operator/cmd/main.go`](https://github.com/D4NS3U/cbse/blob/c993397/experiment-operator/cmd/main.go) — the operator binary: watches `SimulationExperiment` CRs and reconciles children.
- [`scenario-manager/cmd/main.go`](https://github.com/D4NS3U/cbse/blob/c993397/scenario-manager/cmd/main.go) — the Scenario Manager binary: the NATS server, DB state machine, and Job orchestrator.
- [`test/e2e/manifests/`](https://github.com/D4NS3U/cbse/blob/c993397/test/e2e/manifests/base/stack.yaml) — the stack-level manifests (SM, NATS, core DB) the harness deploys alongside the operator.

## High-Level Architecture

A Kubernetes operator pattern with a NATS/JetStream data plane: the CR is the single source of truth; the operator materializes the per-experiment component set; the SM owns the scenario lifecycle, consuming design batches from the EDS, dispatching translation, scheduling runner Jobs, ingesting results, and aggregating the CR's phase/verdict from terminal state plus finalization. See [architecture.md](architecture.md) for the system diagram and the numbered data flow, and [diagrams/sequences.md](diagrams/sequences.md) for the end-to-end workflows.

## Module Map

| Module | Purpose |
|---|---|
| [architecture.md](architecture.md) | System shape, control/data plane, the master flowchart |
| [modules/communication-interfaces.md](modules/communication-interfaces.md) | **The interface reference**: every NATS subject, every database contract, the CR contract |
| [modules/scenario-manager.md](modules/scenario-manager.md) | The SM deep-dive: 23 internal packages, Core DB state machine, finalization, Job lifecycle |
| [modules/experiment-operator.md](modules/experiment-operator.md) | The operator deep-dive: alpha4 CRD field tables, reconcile flow, child inventory |
| [modules/experimental-design-service.md](modules/experimental-design-service.md) | The reference EDS: REST API, SBO publisher wire, recipe store, self-provision |
| [modules/translator.md](modules/translator.md) | The recipe → runner-code pipeline and image build |
| [modules/simulation-runner.md](modules/simulation-runner.md) | The generated runner: model, seeds, result writes, Job execution |
| [modules/post-processing-service.md](modules/post-processing-service.md) | The statistical evaluation policy (CI half-width semantics) and its wire |
| [modules/scenario-databases.md](modules/scenario-databases.md) | The experiment-scoped databases: six-object design schema, result tables |
| [diagrams/class-diagram.md](diagrams/class-diagram.md) | The core domain types across the wire |
| [diagrams/sequences.md](diagrams/sequences.md) | End-to-end sequence diagrams: provisioning, design, execution round, finalization |

## Getting Started

See [getting-started.md](getting-started.md).
