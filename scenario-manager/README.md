# CBSE Scenario Manager

The Scenario Manager is CBSE's experiment lifecycle owner. It keeps the
authoritative scenario state in the Core PostgreSQL database, ingests EDS
scenario batches, drives the translation handoff, orchestrates the runner
Jobs, observes their outcomes, and performs deletion-time cleanup. The
[Experiment Operator](../experiment-operator/README.md) provisions the
experiment's workloads; everything scenario-lifecycle happens here.

## Responsibilities

- **EDS intake.** Answers the EDS availability handshake on the
  `cbse.<namespace>.<project>.eds.scenarios.available` subject and consumes
  scenario batches from the JetStream stream `cbse_eds_scenarios`, inserting
  each batch into the per-scenario `scenario_status` table in one
  transaction.
- **Translation-request selection.** One serial worker loop discovers the
  lowest positive `Created` scenario, claims it, and publishes the
  translation request on `cbse.<namespace>.<project>.trans.request`
  (stream `cbse_translator`) only after a durable JetStream publish
  acknowledgement. Each iteration first recovers stale unpublished
  claims.
- **Translator-ready handling.** Consumes ready messages on
  `cbse.<namespace>.<project>.trans.<scenario-id>.ready`, validates the
  returned digest against the live experiment's
  `spec.translator.repository`, and applies the guarded
  `Scheduled -> StartingRunners` transition; an empty image consumes the
  attempt through the recovery path.
- **Runner-Job orchestration.** A bounded, ordered scheduler discovers
  `StartingRunners` scenarios in ascending ID order and creates (or
  confirms) the deterministic runner Job
  `simrun-<UID-prefix>-s<scenario-id>-a<attempt>` from the
  Operator-validated runner template, the accepted runner digest, and the
  deterministic `simrunner-<UID-prefix>` ServiceAccount.
- **Observation.** A per-scenario deduplicated queue observes each
  `InProcessing` scenario's runner Job on a fixed five-second interval,
  records the computed repetition count in the scenario row, and applies the
  guarded `InProcessing -> PostProcessing` transition on completion or
  `InProcessing -> Failed` on Job failure, collision, or forbidden access.
- **Post-processing boundary.** `PostProcessing` is a scenario state entered
  after runner completion; no component consumes it yet, and there is no
  PostProcessingService call contract.
- **Lifecycle dispatch.** A cluster-wide informer over the alpha4
  `SimulationExperiment` closes the per-experiment lifecycle gate, runs the
  terminal action (move unfinished scenarios to `Failed`) for failed
  experiments, and performs deletion cleanup (ownership-verified runner Job
  deletion, per-experiment Translator consumer deletion, ready-subject
  purge).

Startup is fail-fast: configuration, messaging templates and stream names,
Core DB schema, Kubernetes authorization, and the authentication-free NATS
connection are all validated before any consumer or scheduler starts, and any
startup configuration failure exits the process (see
`internal/core.RunScenarioManager`).

## Package map

`internal/` contains one package per responsibility:

| Package | Responsibility |
| --- | --- |
| `communication` | Transport-neutral `alpha4` wire types (EDS availability/batch, translation request, Translator ready), payload validation, and the publisher/handler surfaces between core workflows and broker adapters |
| `config` | Startup configuration: the runner-start worker count (1–64, default 4); the observation scheduler's fixed four workers are not configurable |
| `core` | Composition entry point: startup validation, NATS/JetStream connection, stream and consumer reconciliation, and construction and start of the informer, adapters, selection loop, and schedulers |
| `effectivejob` | Builds the effective batch/v1 runner Job by merging the Operator-validated (or default) template with the Scenario Manager-controlled fields |
| `eventlog` | Scenario-observability logger: one Job creation/adoption record and one terminal outcome record per scenario, credential-free |
| `informer` | Cluster-wide alpha4 `SimulationExperiment` informer and lifecycle dispatch (terminal, completed, and deletion-cleanup actions on the fixed five-second retry cadence) |
| `jobadapter` | The batch/v1 Kubernetes Job adapter behind the scheduler boundary: create/confirm, observe, `completedIndexes` parsing, and gate-race cleanup |
| `kube` | Legacy Kubernetes API client helpers, exercised only by this package's own tests; the active composition builds its client directly |
| `lifecycle` | Experiment lifecycle gate, finalizer sequencing, terminal action, deletion cleanup, and the deterministic runner-identity derivation |
| `nats` | Canonical `alpha4` messaging contract: subject templates, the two shared JetStream streams, the two Scenario Manager durable consumers, and per-experiment Translator consumer configuration and ownership verification |
| `observation` | Per-scenario deduplicated observation queue with the guarded `InProcessing -> PostProcessing`/`Failed` transitions |
| `persistence` | Core DB persistence: the namespace/name-keyed project table, the publication-boundary scenario-status transitions, and the terminal-action bulk update |
| `rbac` | Startup authorization checks via `SelfSubjectAccessReview`; a denied check is a fatal installation error |
| `ready` | The Translator-ready workflow: digest validation, exact repository match, guarded `Scheduled -> StartingRunners`, and the empty-image attempt recovery path |
| `registry` | Cross-module copy of the Operator's image-reference and Docker-config resolver; revalidates the persisted runner digest and the live registry-auth Secret at runner start |
| `runnerstart` | Bounded ordered runner-start scheduler with process-local ready/delayed/in-flight state and transient-failure delay |
| `scheduler` | Resource-neutral boundary between core orchestration and the workload adapter that creates, confirms, observes, and deletes runner Jobs |
| `selection` | The translation-request selection loop: one serial worker owning the `Created -> Scheduled` transition and the publication boundary |
| `subject` | The canonical namespace-aware `alpha4` NATS subject grammar and its strict parser |
| `translatorconfig` | Centralized translator-workflow configuration shared by packages without import cycles (attempt policy, publish-recovery timeout) |

## Development

The repository-root [`make test-fast`](../AGENTS.md) is the hermetic test
tier; it runs the Scenario Manager's race-enabled unit suite
(`go test -race ./...`) plus the compile-only integration-tagged check
together with the operator and translator modules and the harness
self-tests.

Within the module:

```sh
cd scenario-manager
go test ./...    # unit + integration suites
```

Cluster-level behavior (real PostgreSQL, NATS/JetStream, and the reference
components) is covered by the smoke suite in the
[testing guide](../docs/CBSE_TESTING_GUIDE.md); the runtime cluster
prerequisites, including the `UserNamespacesSupport` feature gate required by
the reference Translator's rootless BuildKit sidecar, are stated in
[docs/CLUSTER_REQUIREMENTS.md](../docs/CLUSTER_REQUIREMENTS.md). For the
EDS and Translator message contracts this manager implements, see
[Designing Custom CBSE Components](../docs/COMPONENT_DESIGN_GOALS.md).

## License

Copyright 2025-2026 Daniel Seufferth.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
