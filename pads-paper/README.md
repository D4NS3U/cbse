# PADS paper component workspace

This directory is the **only ownership root** for paper-specific model and
component implementations on the `PADS-paper` branch. Every paper-owned
source file, its local documentation, and paper-specific packaging live under
`pads-paper/`; the shared repository templates remain unchanged baselines that
later slices copy into their paper-local directories. Nothing outside this
directory is modified to implement the paper model.

## Baseline provenance

The baseline reference commit for every template import is
`1dfc7eb5fec358199cb9c28b1df2aaabb035c71a`. Later slices must import from the
baseline tree at that commit and **preserve source provenance**: each paper-local
copy must remain recognizably derived from its named baseline, institution
attribution and existing license headers must survive the import, and every
deviation from the baseline must be traceable to an explicit requirement in the
normative documents below. P3A imported the named baseline trees into the
paper-local implementation directories; the exact source-to-destination
mappings, the selected canonical model files, the Result Database exception,
and the reproducible parity checks are recorded in
[TEMPLATE_BASELINE.md](TEMPLATE_BASELINE.md).

## Template-reuse ruling (mandatory)

The root README and every component README in this tree carry the following
binding programming rule:

> Reuse the already existing CBSE templates and extend them only with the parts
> necessary for the impatient-customer multi-server queue (M/M/c+M, Erlang-A)
> model. Do not replace a template with a clean rewrite, parallel framework, or
> speculative abstraction.

## Component inventory

| Directory | Responsibility | Required baseline |
|---|---|---|
| `simulation-model/` | The pure SimPy `erlang_a_v1` model and result-record construction that every runner replication executes | `component-templates/translator/internal/generator/runnermod/` |
| `simulation-model/container/` | The runner-base image contract and the generated-runner Dockerfile contract that host one replication | `component-templates/translator/runner-base/` and the generated-runner Dockerfile contract |
| `experimental-design-service/` | Paper experiment design and delivery: parameter entries, recipes, and scenario batches | `component-templates/experimental-design-service/` |
| `translator/` | Scenario translation: Detail DB lookup, generated paper model/result modules, image build and digest-pinned publication | `component-templates/translator/` |
| `post-processing-service/` | The confidence-interval-driven adaptive replication loop over `mean_wait_time` | `component-templates/post-processing-service/` |
| `scenario-detail-database/` | The experiment design store: four entry tables, recipes locator, and the `simulation_parameters` projection | `component-templates/scenario-detail-database/` |
| `result-database/` | The authoritative store of `erlang_a_v1` JSONB result rows | Existing official PostgreSQL Result DB contract; there is no standalone repository Result DB template |

## Shared framework (outside this directory, unchanged)

The Experiment Operator, the Scenario Manager, the alpha4 CRD, and the
repository test harness remain outside `pads-paper/` and are **reused
unchanged**. A later, explicitly approved user ruling is the only path to a
paper-local change to any shared surface; a discovered need to modify one is a
design contradiction, not something this directory absorbs.

## Normative documents

- [Feature specification](../devlog/changes/pads-paper/FEATURE.md)
- [Model selection and rationale](../devlog/changes/pads-paper/MODEL_SELECTION.md)
- [Initial-results plan (Experiments A–C)](../devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md)
- [P2 slice specification (this workspace)](../devlog/changes/pads-paper/slices/P2-paper-component-workspace.md)

These documents are read-only inputs: they define the `erlang_a_v1` model
contract, the preserved CBSE interfaces, the template-reuse ruling, and the
planned experiments.

## Hygiene

- No infrastructure values, credentials, private registry names, image tags,
  endpoints, or kubeconfig paths belong in this tree; placeholders and
  environment-supplied values only.
- Institution attribution and existing licenses must be preserved when code is
  imported in later slices.
- Nothing in this tree may claim that the Erlang-A implementation or any paper
  experiment has run.

## Status

**P3A baseline import complete — behavior unchanged.** The P2 ownership
boundary and provenance documentation remain the foundation of this
directory. P3A imported the named baseline trees byte-identically into the
`component/` directories, `simulation-model/src/`, and
`simulation-model/container/base/`; the record is
[TEMPLATE_BASELINE.md](TEMPLATE_BASELINE.md). The imported trees are **dormant
source snapshots**: no paper-local module is registered in `go.work`, no
imported Dockerfile is built or selected by any manifest, harness, or
deployment path, and the Erlang-A implementation does not yet exist.
Behavioral work begins in P3B; this document makes no
implementation-complete claim.
