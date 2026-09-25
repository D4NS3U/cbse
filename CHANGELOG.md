# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/).
CBSE is a research milestone-driven project: formal releases with a
[Semantic Versioning](https://semver.org/spec/v2.0.0.html) cadence are planned
follow-up work (the release pipeline, Package B of the repository
professionalization program), and until then entries are grouped per research
milestone. The Keep-a-Changelog subsection semantics (Added / Changed /
Deprecated / Removed) are kept from the start so the release process can extend
this file without rework.

## [Unreleased]

### Added

- `scenario-manager/README.md`: the Scenario Manager module README with its
  lifecycle responsibilities, the `internal/` package map, and development workflow.
- `alpha4` as the only active `SimulationExperiment` API version: the CRD serves
  and stores only `alpha4`, targeting Kubernetes 1.30 or newer.
- Reference Translator (`component-templates/translator/`): consumes translation
  requests, looks up the scenario's model parameters in the Scenario Detail
  Database, generates a SimPy-based simulation runner, builds it through a
  rootless BuildKit sidecar, and publishes the runner image as an immutable
  digest-pinned reference.
- Runner Job orchestration: `simrun-*` Kubernetes Jobs run the digest-pinned
  runner image non-root, one indexed repetition per requested repetition with a
  distinct seed; result rows persist in the experiment's per-scenario
  PostgreSQL Result DB tables.
- Reference Scenario Detail Database image
  (`component-templates/scenario-detail-database/`) with the
  `public.simulation_parameters` schema.
- Smoke-verified end-to-end chain: operator provisioning, EDS intake via
  NATS/JetStream, translation, runner execution, and result persistence
  (5/5 Ginkgo specs, including the real Translator chain with real Result DB
  rows).
- Documentation refresh: per-package documentation for the operator, Scenario
  Manager, and reference Translator; harness, mock, and e2e documentation; the
  testing guide and project status re-anchored to the current architecture.

### Changed

- The Translator module's race suite now runs with a 30-minute per-binary test
  timeout because cold-cache hosts could exhaust Go's 10-minute default under
  race instrumentation, killing healthy test binaries (M0 review finding).
- `docs/COMPONENT_DESIGN_GOALS.md` is refreshed to the live `alpha4` contract: the only
  served/stored API version, `experiment.cbse.terministic.de/alpha4` CR examples, the
  namespace-aware subject grammar, and the nested-only image layout.
- `experiment-operator/README.md` replaced its kubebuilder scaffold with the real
  module documentation (alpha4 reconciler, provisioning, envtest development flow),
  and the image-layout descriptions in `component-templates/translator/README.md`
  and `test/e2e/README.md` now state the nested-only truth everywhere.
- `CONTRIBUTING.md` now states the research-stage contribution policy: CBSE is an
  active PhD project, pull requests are not yet accepted, and contributing opens
  with the first stable release — `CONTRIBUTING.md` itself is the marker of that
  change.
- Test harness rework: isolated namespace per run, Kubernetes Lease
  serialization, run-scoped diagnostics under `artifacts/test/<run-id>/`, and
  self-cleanup; harness self-tests are part of the hermetic tier.
- README and project status refreshed to the alpha4 product state.

### Removed

- Retired `experiment-operator/config/samples/` alpha1/alpha2-era sample manifests
  (one describing a private database host address). The active API is `alpha4`-only;
  curated samples can return with the first stable release if useful.
- The `.pi/` agent-session configuration directory is untracked and gitignored;
  worker model pinning uses the explicit launch command path (`pi --model …`),
  which reads no project settings.
- The retired `alpha2` and `alpha3` `SimulationExperiment` API versions are no
  longer active; `alpha4` is the only version served and stored. (Deletion of
  the retired source trees is tracked as a separate repository-hygiene step.)

## [v0.1-jos-paper] - 2026-02-16

The only version tag in the repository — the paper-linked anchor for the state
CBSE had when the project's publication work was finalized: an Experiment
Operator managing `SimulationExperiment` resources, a Scenario Manager keeping
project and scenario state, and NATS-based EDS scenario communication.

### Added (milestone state)

- Experiment Operator managing `SimulationExperiment` custom resources.
- Scenario Manager with project and scenario persistence (PostgreSQL).
- EDS scenario batch communication over NATS.
- Apache-2.0 `LICENSE` and the publications-based README framing.

## Pre-tag history

### Early foundations (November 2025 – February 2026)

- In-cluster test environments for the operator and Scenario Manager;
  `SimulationExperiment` informer; Scenario Manager bootstrap and connection
  checks.
- EDS NATS communication and Scenario Manager project-tree restructure
  (January 2026).

### alpha2 → alpha3 API evolution (September 2025 – July 2026)

- **alpha1/alpha2 (September 2025):** initial repository; the
  `SimulationExperiment` API introduced as alpha1, then promoted to alpha2 with
  ServiceType selection and operator reconciliation (merged as pull request #1).
- **alpha3 (June – July 2026):** the API and the Scenario Manager were updated
  to `alpha3` (merged as pull request #4); the first Translator–Scenario
  Manager communication specification was implemented over NATS; the basic
  scenario selection loop (BSSL) landed (merged as pull request #5); the
  testing pipeline was reworked with mock EDS/Translator components, a fully
  retained end-to-end profile, and expanded testing documentation.
