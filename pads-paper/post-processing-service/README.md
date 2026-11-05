# PADS paper — Post-Processing Service

Paper-local home for the Post-Processing Service (PPS) component that drives
the adaptive replication loop.

## Baseline

`component-templates/post-processing-service/`

## Template-reuse ruling (mandatory)

> Reuse the already existing CBSE templates and extend them only with the parts
> necessary for the impatient-customer multi-server queue (M/M/c+M, Erlang-A)
> model. Do not replace a template with a clean rewrite, parallel framework, or
> speculative abstraction.

## Role in Experiments A–C

The PPS consumes the `mean_wait_time` observations produced by the
`erlang_a_v1` replications in **Experiments A and B**
([initial-results plan](../../devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md)),
evaluates the confidence-interval precision target after each runner round,
and requests additional replications until the target is met or the
replication cap is reached. It reports the replication counts and round sizes
that the paper uses as its orchestration-behavior evidence. **Experiment C**
is offline replay and does not involve the PPS.

## Preserved baseline contracts (must survive template import)

- **Primary metric.** `mean_wait_time` remains the KPI; the PPS reads only
  that field from each result row.
- **Stopping rule.** The absolute 95% confidence-interval half-width
  (Student-t) over the pooled observations must be at most the scenario's
  `confidence_metric` ε; the replication cap remains the user-defined
  secondary stopping criterion.
- **Wire contract.** The evaluation request/response payloads, including the
  echoed `confidence_metric` and the `metric` field, are unchanged.

## Expected Erlang-A-specific extension area

None in the statistical logic. The PPS must simply tolerate the additive
result fields introduced by `erlang_a_v1`; accepting them is required, and
redefining the primary KPI or the confidence-interval policy is forbidden.

## Preserved interfaces (must not change in this directory)

- The `mean_wait_time` extraction and pooling semantics;
- The absolute 95% confidence-interval half-width stopping algorithm;
- The evaluation wire payloads.

## Status

**foundation only — implementation begins in P3 or later.** This directory is
an ownership placeholder; it currently contains no service code.
