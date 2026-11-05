# PADS paper — Result Database

Paper-local home for the authoritative store of `erlang_a_v1` result rows.

## Baseline

The existing official PostgreSQL Result DB contract. **There is no standalone
repository Result DB template**; the ruling below therefore binds this
component to keep that existing contract intact rather than to import a
template.

## Template-reuse ruling (mandatory)

> Reuse the already existing CBSE templates and extend them only with the parts
> necessary for the impatient-customer multi-server queue (M/M/c+M, Erlang-A)
> model. Do not replace a template with a clean rewrite, parallel framework, or
> speculative abstraction.

## Role in Experiments A–C

The Result Database persists every `erlang_a_v1` result row produced by
**Experiments A and B**
([initial-results plan](../../devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md)),
supplies the `mean_wait_time` observations the PPS pools, and provides the
persisted rows (parameters, effective seed, metrics) that **Experiment C**
replays and compares byte-for-byte.

## Preserved baseline contracts (must survive template import)

- **Official PostgreSQL image.** The database runs as the existing official
  PostgreSQL deployment; the repository owns no initialization SQL for it.
- **No repository-owned schema.** Tables are not pre-created by the
  repository; the runner creates its `scenario_<id>_results` JSONB table on
  first use inside its single transaction (advisory lock, `IF NOT EXISTS`),
  and those runner-created tables remain authoritative.
- **Additive JSONB fields.** The paper's additional result fields
  (`model_id`, served/abandoned/total counts, abandonment probability,
  throughput, utilization) are JSONB payload fields; no Result DB schema
  migration is required or permitted by the paper.

## Expected Erlang-A-specific extension area

None. The paper adds no schema, no migration, and no repository-owned
packaging to this component; later slices may add only paper-local
documentation or packaging actually required.

## Preserved interfaces (must not change in this directory)

- The runner's single-transaction table-creation-and-insert contract;
- The official PostgreSQL image contract;
- The JSONB row shape as consumed by the PPS (additive fields only).

## Status

**foundation only — implementation begins in P3 or later.** This directory is
an ownership placeholder; it currently contains no schema, migration, or
image inputs.
