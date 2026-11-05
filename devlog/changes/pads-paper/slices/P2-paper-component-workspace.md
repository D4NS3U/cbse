# P2 — Paper component workspace foundation

## Target

Create the repository-root `pads-paper/` ownership boundary and documented subdirectories for every PADS-paper-specific component. This slice creates structure and programming instructions only; it does not import or modify component code yet.

## Dependency

- P1 model selection and initial-results documentation is complete.

## Requirements

### P2-R1 — Exact source-tree boundary

Create this tracked tree:

```text
pads-paper/
├── README.md
├── simulation-model/
│   ├── README.md
│   └── container/
│       └── README.md
├── experimental-design-service/
│   └── README.md
├── translator/
│   └── README.md
├── post-processing-service/
│   └── README.md
├── scenario-detail-database/
│   └── README.md
└── result-database/
    └── README.md
```

No symlinks, source files, generated files, Dockerfiles, copied modules, or vendored dependencies belong in P2. P3 owns template import and executable code.

### P2-R2 — Root ownership and inventory document

`pads-paper/README.md` must:

- state that this directory is the only ownership root for paper-specific model and component implementations;
- list all six component/model directories and their responsibilities;
- state that the shared Experiment Operator, Scenario Manager, alpha4 CRD, and test harness remain outside this directory and are reused unchanged unless a later explicit user ruling says otherwise;
- link the normative feature, model-selection, and initial-results documents under `devlog/changes/pads-paper/`;
- record the baseline reference commit `1dfc7eb5fec358199cb9c28b1df2aaabb035c71a` and explain that later imports must preserve source provenance;
- state that placeholders are not runnable components and make no implementation-complete claim.

### P2-R3 — Mandatory template-reuse ruling

The root README and every component README must carry this programming rule in equivalent unambiguous language:

> Reuse the already existing CBSE templates and extend them only with the parts necessary for the impatient-customer multi-server queue (M/M/c+M, Erlang-A) model. Do not replace a template with a clean rewrite, parallel framework, or speculative abstraction.

Each component README must name its exact baseline:

| Paper directory | Baseline |
|---|---|
| `simulation-model/` | `component-templates/translator/internal/generator/runnermod/` |
| `simulation-model/container/` | `component-templates/translator/runner-base/` and the generated-runner Dockerfile contract |
| `experimental-design-service/` | `component-templates/experimental-design-service/` |
| `translator/` | `component-templates/translator/` |
| `post-processing-service/` | `component-templates/post-processing-service/` |
| `scenario-detail-database/` | `component-templates/scenario-detail-database/` |
| `result-database/` | Existing official PostgreSQL Result DB contract; there is no standalone repository Result DB template |

### P2-R4 — Component programming boundaries

Each README must concisely identify:

- the component's role in Experiments A–C;
- the baseline behavior/contracts that must survive template import;
- the expected Erlang-A-specific extension area;
- preserved interfaces that must not change in that directory;
- current status: `foundation only — implementation begins in P3 or later`.

Specific boundaries:

- **Simulation model:** preserve injected local RNG, effective-seed recording, one-result-per-replication, and no event-level logging; later add the two-server exponential-patience model and paper metrics.
- **Model container:** preserve immutable base-image input, SimPy 4.1.2, Psycopg lock, numeric UID/GID 1000, and no embedded credentials.
- **EDS:** preserve the four fields `arrival_rate`, `service_rate`, `run_duration`, and `seed_policy`; no generic parameter designer.
- **Translator:** preserve Detail DB lookup, BuildKit/registry security, generated-image digest verification, and Result DB connection handling; later generate the paper model/result modules.
- **PPS:** preserve `mean_wait_time` and the absolute 95% CI half-width stopping rule; later accept additive metrics without redefining the primary KPI.
- **Scenario Detail Database:** preserve the four entry tables, recipes locator, and `simulation_parameters` projection; only paper experiment fixtures/entries may be added later.
- **Result Database:** preserve official PostgreSQL with no repository-owned initialization schema; runner-created `scenario_<id>_results` JSONB tables remain authoritative.

### P2-R5 — Scope and hygiene

- Do not edit `component-templates/**`, shared runtime code, CRDs, manifests, the root Makefile, or tests.
- Do not add infrastructure values, credentials, image tags, endpoints, kubeconfig paths, or generated artifacts.
- Do not claim that the Erlang-A implementation or any paper experiment has run.
- Preserve institution attribution and existing licenses when code is imported in later slices.

## Observable acceptance

1. `find pads-paper -type f | sort` reports exactly the eight README files required by P2-R1.
2. Every README names its baseline and contains the template-reuse ruling or an unambiguous equivalent.
3. `rg` confirms all required preserved contracts and status language are documented.
4. `git diff --check` passes.
5. `make test-fast` passes from the repository root; documentation-only P2 does not require cluster smoke.
6. The implementation commit contains the required orchestration task/dispatch trailer and edits only `pads-paper/**`.

## Completion

P2 is `complete` only when an independent Pi/GLM review returns `approve` against the implementation HEAD and the approved child branch is landed on `PADS-paper`. Otherwise it is `incomplete` or `verification-blocked`.
