# PADS paper — simulation model

Paper-local home for the `erlang_a_v1` SimPy model: the discrete-event
simulation each runner replication executes, and the strict result-record
construction that turns its outputs into the JSONB row the runner persists.

## Original implementation

The Erlang-A event logic in [`src/model.py`](src/model.py) is an **original
CBSE implementation** written independently from the documented queueing
primitives (Poisson arrivals, parallel exponential servers, exponential
patience) and the SimPy 4.1.2 API (`Resource`, timeout events, event
conditions). The published SimPy *Bank Renege* example and the Erlang-A
literature (Garnett, Mandelbaum and Reiman, 2002; Mandelbaum and Zeltyn;
Whitt, 2006) serve as behavioral and citation anchors only — no third-party
example source is copied, and no source comment claims copied lineage. The
Apache-2.0 headers of the imported baseline are retained.

## Model: finite-horizon two-server M/M/c+M (`erlang_a_v1`)

Fixed, versioned constants (declared in `src/model.py`; changing one
requires a new model version):

| Constant | Value | Meaning |
|---|---:|---|
| `MODEL_ID` | `erlang_a_v1` | Recorded in every result row |
| `SERVERS` (`c`) | 2 | Parallel servers, FCFS via `simpy.Resource` |
| `PATIENCE_RATE` (`θ`) | 1.0 | Mean patience: one simulation time unit |
| `WARMUP_FRACTION` | 0.10 | Fraction of `T` used as warm-up floor |
| `MINIMUM_WARMUP` | 10.0 | Warm-up floor in time units |

## Event and measurement semantics

For one replication (`model.run(arrival_rate, service_rate, run_duration, rng)`):

1. Admit Poisson arrivals (interarrival times drawn from the injected RNG)
   from time 0 until `measurement_end`; an arrival sampled at or after the
   boundary is not admitted.
2. Run an unmeasured warm-up of `max(MINIMUM_WARMUP, WARMUP_FRACTION ×
   run_duration)` time units. Warm-up customers occupy the queue and the
   servers and count toward server utilization, but are excluded from
   measured-customer counts and waits.
3. Measure customers arriving in
   `[measurement_start, measurement_end)`, where
   `measurement_start = warm-up` and the window has length `run_duration`.
4. Stop generating arrivals at `measurement_end`, then drain every admitted
   customer process to service completion or abandonment.

Each admitted customer samples exponential patience at `PATIENCE_RATE` and
exponential service at `service_rate` from the injected RNG, then races the
`simpy.Resource(capacity=SERVERS)` request against the patience timeout. A
granted request enters service immediately; a patience timeout first cancels
the pending request, so an abandoned customer consumes no capacity. An exact
tie between grant and deadline (probability zero under the continuous RNG)
settles for one additional event step and resolves in favor of the granted
request. No event-level printing or logging occurs; output volume stays
proportional to replications, not simulation events.

## Metric definitions

`model.run` returns exactly these fields, in this order:

| Field | Definition |
|---|---|
| `model_id` | `erlang_a_v1` |
| `completed_customers` | Compatibility alias; equal to `served_customers` |
| `served_customers` | Measured customers that entered and completed service |
| `abandoned_customers` | Measured customers whose patience expired first |
| `total_customers` | `served_customers + abandoned_customers` |
| `mean_wait_time` | Primary PPS metric: mean time from measured arrival until service start or abandonment, over all measured customers; `0.0` when none arrive |
| `abandonment_probability` | `abandoned_customers / total_customers`; `0.0` for an empty measured set |
| `throughput` | `served_customers / run_duration` |
| `server_utilization` | Total service-busy overlap with the measured window (including warm-up customers) divided by `SERVERS × run_duration`, clamped to `[0, 1]` against floating-point boundary noise |

Counts are non-negative integers; aggregates are finite floats; probability
and utilization lie in `[0, 1]`.

## Result builder

`src/result.py` provides the strict production result builder
`result.build_result(scenario, metrics, effective_seed)`:

- `scenario` carries exactly the baked keys `parameterset_id`,
  `arrival_rate`, `service_rate`, `run_duration`, and `seed_policy`;
- `metrics` is the exact `model.run` dict;
- the record enumerates the deterministic output order of
  `result.RESULT_FIELDS` (scenario fields, `effective_seed`, then the metric
  fields) and carries no `scenario_id` and no database credentials.

The builder rejects missing or unexpected fields, non-finite floats,
inconsistent accounting (`served + abandoned ≠ total`), alias mismatch
(`completed ≠ served`), and probability/utilization outside `[0, 1]` rather
than persisting malformed data. `src/seed.py` is unchanged: production
effective-seed derivation remains `seed_policy + hostname` (63-bit
`sha256`) until a separately approved ruling changes it.

## Host CLI (credential-free)

`src/cli.py` runs one replication on the host and emits exactly one compact,
key-sorted JSON object plus a newline to stdout:

```text
python3 cli.py --arrival-rate <INTEGER> --service-rate <INTEGER> \
    --run-duration <INTEGER> --seed <INTEGER>
```

The first three values are positive integers; `--seed` is a non-negative
integer and is used directly as the effective seed via
`random.Random(seed)`. The output contains `arrival_rate`, `service_rate`,
`run_duration`, `effective_seed`, and all exact model metric fields. The CLI
accepts no credentials, database configuration, hostname derivation, NATS,
or other infrastructure input; diagnostics go to stderr and invalid input
exits non-zero with empty stdout.

### Execution surfaces (distinct, do not conflate)

| Surface | Status | Role |
|---|---|---|
| Host CLI (`src/cli.py`) | implemented in P3B1 | Credential-free one-replication replay from an explicit effective seed; one JSON object to stdout |
| Standalone model container | P3B2 (not implemented) | Immutable-base, credential-free application image wrapping the accepted CLI; still no database access |
| PostgreSQL production runner | P4 (not implemented) | Translator-generated runner: baked scenario parameters, hostname-derived effective seed, and the single JSONB result insert into the Result DB |

## Reproducibility limitation

Live production effective seeds remain hostname-derived
(`seed_policy + hostname` via `src/seed.py`), so a second Kubernetes
execution of the same scenario is not guaranteed to receive the same seed.
Recorded effective seeds are persisted in every result row and can be
replayed deterministically through the host CLI (or
`random.Random(effective_seed)` with the recorded parameters); the
reconstructed result must match the persisted row exactly. Exact
whole-experiment replay is not promised by the current hostname-derived seed
contract and must be stated as a limitation unless that contract is
separately redesigned.

## Baseline and provenance

`component-templates/translator/internal/generator/runnermod/` established
the baseline API/security contracts. P3A imported the six selected canonical
files into [`src/`](src/), byte-identical to the
[template baseline provenance](../TEMPLATE_BASELINE.md); P3B1 replaced the
single-server queue in `src/model.py` with the original `erlang_a_v1`
implementation, made `src/result.py` the strict builder for it, and added
the credential-free `src/cli.py` plus tests. `src/seed.py` and
`src/test_seed.py` are unchanged.

## Template-reuse ruling (mandatory)

> Reuse the already existing CBSE templates and extend them only with the
> parts necessary for the impatient-customer multi-server queue (M/M/c+M,
> Erlang-A) model. Do not replace a template with a clean rewrite, parallel
> framework, or speculative abstraction.

## Role in Experiments A–C

The model is the scientific core of all three experiments
([initial-results plan](../../devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md)):

- **Experiment A — load/service response surface:** one replication per
  runner execution produces the waiting, abandonment, throughput, and
  utilization observations across the λ/μ grid at fixed duration and seed
  policy.
- **Experiment B — precision sensitivity:** the same model, re-executed
  across adaptive replication rounds until the PPS precision target is met
  for the boundary recipe.
- **Experiment C — recorded-seed replay:** the model invocation is replayed
  outside Kubernetes from a persisted row using `random.Random(effective_seed)`
  with the recorded parameters; the reconstructed result must match the
  persisted row exactly.

## Preserved contracts (must survive later integration)

- **Injected local RNG only.** The model receives the caller-created
  `random.Random(effective_seed)` instance (the CLI passes
  `random.Random(seed)`) and must never use Python module-global random
  state; `src/seed.py` keeps the 63-bit hostname derivation for production.
- **Effective-seed recording.** `result.build_result` carries the baked
  parameters and the `effective_seed` in every persisted row.
- **One result per replication.** A runner writes exactly one finite JSON
  result or exits non-zero — no partial, NaN, or Inf results; the CLI emits
  one JSON object with `allow_nan=False`.
- **No event-level logging.** Output volume stays proportional to
  replications, not simulation events.
- **Model API shape.** `model.run(arrival_rate, service_rate, run_duration,
  rng)` remains a pure model-facing API over SimPy and the injected RNG.

## Status

**P3B1 implemented on the dedicated child branch.** [`src/`](src/) contains
the canonical `erlang_a_v1` implementation (original Erlang-A event logic),
the strict `result.py` builder, the unchanged `seed.py`, and the
credential-free deterministic `cli.py` with unit/invariant/ensemble tests.
Independent review and landing on `PADS-paper` are pending; the standalone
container (P3B2) and the Translator-synchronized production runner (P4) are
not part of this directory yet.
