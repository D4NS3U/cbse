# Development log — feature specifications

This directory is the durable history of CBSE feature development. Features are specified before they are implemented: each subdirectory groups one feature's normative specification, ordered slices, agent prompts, and implementation-routing evidence. Dispatched coding agents treat every specification as **read-only** — they implement against it, never edit it to fit an implementation.

## Standard feature directory layout

The active feature directory (`simrunner-start-and-container-creation/`) is the model layout:

```
<feature>/
├── FEATURE.md                 # Normative, cross-cutting specification; owns all shared contracts
├── IMPLEMENTATION_HANDOFF.md  # Routing evidence: per-slice state + resume notes; updated by each agent run
├── slices/                    # Ordered implementation slices; requirements owned by FEATURE.md
└── prompts/                   # Controller and launcher prompts consumed by coding agents
```

Older entries predate this layout and hold a single specification file.

## Index

| Entry | Feature | Status |
| --- | --- | --- |
| [`post-processing-service/`](post-processing-service/) | PostProcessingService reference component and flow integration: reference PPS with evaluation logic (KPI accuracy vs user-supplied `confidence_metric`), SM evaluation messaging and `Finished` scenario state, additional-runner rounds loop, operator PPS provisioning, smoke e2e — umbrella [FEATURE.md](post-processing-service/FEATURE.md) | **in-progress — spec authored 2026-09-25, awaiting user review + open-question rulings before slice dispatch** |
| [`repo-professionalization/`](repo-professionalization/) | Program charter (`PROBLEM.md`): turn the dev-heavy repository into a professional open-source-ready product repo. Manager-authored `FEATURE.md` program in three packages (public hygiene, public release pipeline, dev-box sunset) — not a worker-facing spec yet | **Consolidated 2026-09-25 (single-repo decision): Package A complete + M0 passed; B/C parked by the user; governance records live in this repo's devlog** |
| [`public-repo-hygiene/`](public-repo-hygiene/) | Package A of the repo-professionalization program ([charter](repo-professionalization/PROBLEM.md)): OSS staples (A1), retired-tree removal (A2), private-infra scrub of tracked defaults and agent-facing prose (A3), cbse-labs preparation manifest (A4, superseded — see category note) — umbrella [FEATURE.md](public-repo-hygiene/FEATURE.md) | **complete and reviewer-verified (2026-09-24): A1–A4 + follow-ups (A2R, A3R, L1, L2, E1, E2) all settled; M0 external-reviewer dry-run passed with documented deviations; D2 relocated then consolidated back 2026-09-25 — see its IMPLEMENTATION_HANDOFF.md** |
| [`simrunner-start-and-container-creation/`](simrunner-start-and-container-creation/) | Alpha4 Simulation Runner startup and on-demand Translator image creation: alpha4 API/CRD, Job-template policy, Operator provisioning, SM messaging and lifecycle, reference Translator runtime, runner-Job orchestration, images/smoke/cutover ([FEATURE.md](simrunner-start-and-container-creation/FEATURE.md)) | **Active — implemented and smoke-verified.** Final group `S07-A3` passed 5/5 Ginkgo specs incl. the real Translator end-to-end chain (see its `IMPLEMENTATION_HANDOFF.md`). Alpha4 is the only active API version; alpha2/alpha3 are retired. |
| [`translator_implementation/`](translator_implementation/) | Alpha3-era Scenario Manager–Translator JetStream communication spec and implementation recap | Implemented (alpha3 era); superseded by the alpha4 reference Translator rework (Slice 05 of the active feature) |
| [`basic-scenario-selection-logic/`](basic-scenario-selection-logic/) | Basic scenario selection logic (BSSL) — the alpha3 selection-loop specification | Implemented (alpha3 era); superseded by the alpha4 selection loop in `scenario-manager/internal/selection/` |
| [`update_to_alpha3_api/`](update_to_alpha3_api/) | alpha2 → alpha3 API update specification | Implemented (alpha3 era) — alpha3 was retired by the alpha4 cutover |
| [`simulationexperiment_update/`](simulationexperiment_update/) | Alpha3-era `SimulationExperiment` update specification | Superseded by the alpha4 `SimulationExperiment` surface of the active feature |
| [`translator_comm.md`](translator_comm.md) | First Translator–SM communication draft (single file) | Superseded — header points to [`translator_implementation/translator_comm_impl_spec.md`](translator_implementation/translator_comm_impl_spec.md) |

## Adding a new feature

1. Follow the root [`MANAGER.md`](../../MANAGER.md): the manager authors the `FEATURE.md` (mission, scope, workflow decisions, Gang of Four design-pattern section, global constraints, slices, verification gates, completion vocabulary) before any implementation starts.
2. Create `changes/<feature>/` in the standard layout above and add the entry to this index with status `in-progress`.
3. Implementation runs through dispatched worker agents; every run updates `IMPLEMENTATION_HANDOFF.md`. Record the settled status here (implemented / superseded) once the feature completes or is retired.
4. Every agent — coordinator and workers — obeys the repository-root [`AGENTS.md`](../../AGENTS.md) test and cluster-safety contract.
