# PADS paper — Scenario Detail Database

Paper-local home for the experiment design store that the paper EDS writes
and the paper Translator reads.

## Baseline

`component-templates/scenario-detail-database/`

## Template-reuse ruling (mandatory)

> Reuse the already existing CBSE templates and extend them only with the parts
> necessary for the impatient-customer multi-server queue (M/M/c+M, Erlang-A)
> model. Do not replace a template with a clean rewrite, parallel framework, or
> speculative abstraction.

## Role in Experiments A–C

The Detail Database holds the design layer for **Experiments A and B**
([initial-results plan](../../devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md)):
the parameter entry tables, the recipes locator, and the
`simulation_parameters` projection that the Translator queries per scenario.
**Experiment C** reads persisted result rows from the Result Database, not
the design layer.

## Preserved baseline contracts (must survive template import)

- **Four entry tables.** `arrival_rate`, `service_rate`, `run_duration`, and
  `seed_policy` with their domain checks (positive, positive, positive,
  non-negative).
- **Recipes locator.** One row per parameter set, with one foreign key per
  parameter.
- **`simulation_parameters` projection.** The exact legacy columns
  (`parameterset_id`, `arrival_rate`, `service_rate`, `run_duration`,
  `seed_policy`) over the recipes join; the landed lookup contract (query
  text, key, four-value projection) is unchanged.
- **Immutable baseline rows.** Baseline ids 1–4 are read-only in all five
  tables; the EDS id-space starts at 5.
- **Official PostgreSQL first-start initialization.** The checked-in SQL is
  executed once by the official image's first-start initialization as the
  configured account; the values are fixed, reproducible image inputs, never
  regenerated at build or run time.

## Expected Erlang-A-specific extension area

Only paper experiment fixtures/entries may be added later — the Experiment
A–C entries, recipes, and scenario definitions, as data. The schema, the
projection, the grants, and the baseline rows are not extension points.

## Preserved interfaces (must not change in this directory)

- The view column shape and the Translator's lookup contract;
- The entry-table and recipes schemas;
- The grant surface and the id-space rule.

## Status

**foundation only — implementation begins in P3 or later.** This directory is
an ownership placeholder; it currently contains no SQL and no image inputs.
