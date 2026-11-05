# PADS paper — Experimental Design Service

Paper-local home for the Experimental Design Service (EDS) component that
designs and delivers the PADS paper experiments.

## Baseline

`component-templates/experimental-design-service/`

## Template-reuse ruling (mandatory)

> Reuse the already existing CBSE templates and extend them only with the parts
> necessary for the impatient-customer multi-server queue (M/M/c+M, Erlang-A)
> model. Do not replace a template with a clean rewrite, parallel framework, or
> speculative abstraction.

## Role in Experiments A–C

The EDS is the interactive entry point for **Experiments A and B**
([initial-results plan](../../devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md)):
it designs the parameter entries, composes the recipes, saves the design into
the paper-local Scenario Detail Database, and publishes the scenario batches.
**Experiment C** creates no new design; it replays persisted Experiment A
rows. The existing entry-sharing and recipe composition are exercised by the
paper experiments without any EDS code change.

## Preserved baseline contracts (must survive template import)

- **Four fixed parameter fields and domains.** The design surface is exactly
  `arrival_rate` (positive integer), `service_rate` (positive integer),
  `run_duration` (positive integer), and `seed_policy` (non-negative
  integer). There is no generic parameter designer, no additional parameter
  set, and no schema change.
- **Experiment lifecycle flow.** The save / start / finish actions with their
  pre-flight parameter-set resolution, availability handshake, scenario-batch
  publish, and irrevocable finalization remain as-is.
- **Byte-pinned wire contract.** The protocol payloads at the shared Scenario
  Manager contract edge, including the verbatim experiment-type echo and the
  per-project availability subject naming, are preserved field-for-field.
- **Detail DB write surface.** Entry rows and recipe locator rows are written
  in one transaction, with EDS-owned ids starting at 5; the immutable
  baseline ids 1–4 stay read-only.
- **Fail-fast configuration.** Startup validation of the environment and
  mounted Secret connection values is unchanged.

## Expected Erlang-A-specific extension area

None in code. The paper experiments (A–C) enter later **as data**: parameter
entries, recipes, and scenario definitions chosen from the existing four-value
design contract. The Erlang-A constants (server count, patience rate, warm-up
policy) deliberately do not appear in the EDS.

## Preserved interfaces (must not change in this directory)

- The four parameter field names and domains;
- The wire payload shapes and validation;
- The Detail DB write contract (entry tables plus recipes locator, id-space
  rule);
- The JSON API and embedded UI flow.

## Status

**foundation only — implementation begins in P3 or later.** This directory is
an ownership placeholder; it currently contains no service code.
