# PADS paper — simulation model

Paper-local home for the `erlang_a_v1` SimPy model: the discrete-event
simulation each runner replication executes, and the pure result-record
construction that turns its outputs into the JSONB row the runner persists.

## Baseline

`component-templates/translator/internal/generator/runnermod/`

## Template-reuse ruling (mandatory)

> Reuse the already existing CBSE templates and extend them only with the parts
> necessary for the impatient-customer multi-server queue (M/M/c+M, Erlang-A)
> model. Do not replace a template with a clean rewrite, parallel framework, or
> speculative abstraction.

## Role in Experiments A–C

The model is the scientific core of all three experiments
([initial-results plan](../../devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md)):

- **Experiment A — load/service response surface:** one replication per runner
  execution produces the waiting, abandonment, throughput, and utilization
  observations across the λ/μ grid at fixed duration and seed policy.
- **Experiment B — precision sensitivity:** the same model, re-executed across
  adaptive replication rounds until the PPS precision target is met for the
  boundary recipe.
- **Experiment C — recorded-seed replay:** the model invocation is replayed
  outside Kubernetes from a persisted row using `random.Random(effective_seed)`
  with the recorded parameters; the reconstructed result must match the
  persisted row exactly.

## Preserved baseline contracts (must survive template import)

- **Injected local RNG only.** `seed.derive_seed(seed_policy, hostname)`
  derives the effective seed; the model receives the caller-created
  `random.Random(effective_seed)` instance and must never use Python
  module-global random state.
- **Effective-seed recording.** `result.build_result` carries the baked
  parameters and the `effective_seed` in every persisted row.
- **One result per replication.** `main.py` reads the baked scenario and
  Result DB configuration files, runs the model once, inserts exactly one
  finite JSONB row, and exits non-zero on any failure — no partial, NaN, or
  Inf results.
- **No event-level logging.** Output volume stays proportional to
  replications, not simulation events.
- **Model API shape.** `model.run(arrival_rate, service_rate, run_duration,
  rng)` remains a pure model-facing API over SimPy and the injected RNG.
- **Result DB client.** `resultdb.insert` keeps the shared `database_endpoint`
  dispatcher contract (host classification, ordered de-duplicated
  resolution, first-successful-dial, shared deadline, separate host/port,
  `sslmode=disable`), the statement timeout, and the single transaction that
  acquires the scenario advisory lock, creates the scenario table with
  `IF NOT EXISTS`, and inserts the row; a timeout or permission failure exits
  non-zero and consumes the Pod attempt, with no inner retry.

## Expected Erlang-A-specific extension area

Replace the single-server queue inside `model.py` with the `erlang_a_v1`
two-server M/M/c+M model: `simpy.Resource(capacity=2)`, exponential patience
at fixed rate θ = 1.0 racing each resource request, an unmeasured warm-up of
`max(10, 0.10 × T)` time units, a measured arrival window of length T, and a
drain phase after arrivals stop that leaves no measured customer
unaccounted for. Extend `result.py` additively with `model_id = erlang_a_v1`,
`served_customers`, `abandoned_customers`, `total_customers`,
`abandonment_probability`, `throughput`, and `server_utilization`, keeping
`completed_customers` (equal to `served_customers`) and `mean_wait_time` as
the preserved primary fields. Every scientific constant is recorded in the
model-version documentation; changing one requires a new model version.

## Preserved interfaces (must not change in this directory)

- The `model.run` signature and pure-function boundary;
- `seed.derive_seed(seed_policy, hostname)` and its 63-bit seed output;
- `result.build_result` record construction with its existing fields
  (additive fields only);
- `resultdb.insert(scenario_id, result_record, db_config)` behavior;
- The `database_endpoint` dispatcher / dns / ipv4 / ipv6 contract;
- The `main.py` startup flow, baked file paths, and failure semantics.

## Status

**foundation only — implementation begins in P3 or later.** This directory is
an ownership placeholder; it currently contains no model code and no tests.
