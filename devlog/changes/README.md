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
| [`experiment-phase-aggregation/`](experiment-phase-aggregation/) | Experiment-level phase aggregation: SimulationExperiment `InProgress -> Finished/Failed` via the Scenario Manager's scenario-aggregate status report and the operator's derived phase transition — design note [component-reports-and-field-ownership.md](experiment-phase-aggregation/notes/component-reports-and-field-ownership.md) documents the Kubernetes field-level-ownership pattern (kubelet/Node, scheduler/Pod precedents) grounding the design | **merged to main 2026-09-30 (merge c993078) — P1-P4 complete via the independent glm review lane (4 approves, 1 request-changes fixed and re-approved; 1 dispatch replaced after a provider 400); settlement smoke 7/7 green (run 20260930154245-59c483, live Finished chain proven); post-merge test-fast rc=0 — see [IMPLEMENTATION_HANDOFF.md](experiment-phase-aggregation/IMPLEMENTATION_HANDOFF.md)** |
| [`experiment-terminal-e2e/`](experiment-terminal-e2e/) | Live e2e proof of the SimulationExperiment's remaining terminal branches: both **Error flavors** (user ruling 2026-10-01: Validation-Error — the pre-creation gate, zero children; Provisioning-Error — the mid-sequence NodePort-conflict failure, partial children persist + GC cascade) and the Failed chain (pending rulings) — design note [terminal-paths-and-triggers.md](experiment-terminal-e2e/notes/terminal-paths-and-triggers.md) holds the code-verified route analysis (the readiness park contract, the three scenario-Failed writers, the pooled-cap stop criterion) | **implemented 2026-10-01 on branch D4NS3U/experiment-terminal-e2e — T1-T3 complete (three Error flavors incl. the E2/R-ruled 3-retry readiness watchdog with the G-ruled PPS gate; the F-crash Failed chain via the job-deadline trigger); independent glm review lane: 3 first-round approves; settlement smoke 11/11 green (run 20261001124216-a59a4a) — the gate caught the operator's missing events RBAC (the PhaseTransition Event was silently Forbidden in production), the suite's timeout budget, and the terminal action's job-cleanup premise; merge to main routed to the user — see [IMPLEMENTATION_HANDOFF.md](experiment-terminal-e2e/IMPLEMENTATION_HANDOFF.md)** |
| [`post-processing-service/`](post-processing-service/) | PostProcessingService reference component and flow integration: reference PPS with evaluation logic (KPI accuracy vs user-supplied `confidence_metric`), SM evaluation messaging and `Finished` scenario state, additional-runner rounds loop, operator PPS provisioning, smoke e2e — umbrella [FEATURE.md](post-processing-service/FEATURE.md) | **implemented (2026-09-29) — waves W1-W4 complete (S1 SM state machine, S2 messaging, S3 operator provisioning, S4 reference PPS, S5 harness/stack, S4R wave-cap semantics Q7, S6 e2e loop spec); settlement smoke 6/6 on the live cluster; S4R-built PPS image rebuilt, pushed, and smoke-verified 6/6 (2026-09-30, run 20260930063546-8a4fe4) — record fully clean** |
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
