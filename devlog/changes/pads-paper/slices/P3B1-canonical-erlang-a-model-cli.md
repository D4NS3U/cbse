# P3B1 — Canonical `erlang_a_v1` model and credential-free CLI

## Target

Replace the imported single-server model under `pads-paper/simulation-model/src/` with CBSE's original finite-horizon two-server M/M/c+M (`erlang_a_v1`) implementation and a deterministic host CLI. This slice does not create a Dockerfile and does not modify the paper-local Translator; those are P3B2 and P4.

## Dependency

- P3A is independently approved and landed at `2b3fdb44bb80164cb480b27ce923ebddb195bbee`.
- User rulings approve the canonical source, original implementation, standalone CLI semantics, and later Translator synchronization.

## Requirements

### P3B1-R1 — Original canonical source

`pads-paper/simulation-model/src/` is the only canonical implementation source. Implement the model independently from the documented queueing primitives and literature; do not copy source code from SimPy's Bank Renege example or another GitHub simulation.

Retain Apache-2.0 headers. Documentation may cite upstream examples and academic sources as behavioral/provenance anchors, but source comments must not claim copied implementation lineage.

### P3B1-R2 — Fixed model constants

Declare explicit module constants in `model.py`:

- `MODEL_ID = "erlang_a_v1"`
- `SERVERS = 2`
- `PATIENCE_RATE = 1.0`
- `WARMUP_FRACTION = 0.10`
- `MINIMUM_WARMUP = 10.0`

A constants change requires a future model version. The measured arrival window has length `run_duration`; warm-up length is `max(MINIMUM_WARMUP, WARMUP_FRACTION * run_duration)`.

### P3B1-R3 — Pure model API and validation

Keep the callable API:

```python
run(arrival_rate, service_rate, run_duration, rng)
```

Inputs `arrival_rate`, `service_rate`, and `run_duration` must be positive integers; booleans are invalid. `rng` is an injected local object with `expovariate(rate)`; no module-global random state is allowed.

Return a dict containing exactly:

```text
model_id
completed_customers
served_customers
abandoned_customers
total_customers
mean_wait_time
abandonment_probability
throughput
server_utilization
```

`completed_customers` is the compatibility alias for `served_customers`. Counts are non-negative integers. Aggregate values are finite floats; probability/utilization are in `[0,1]`.

### P3B1-R4 — Event algorithm

For one replication:

1. Create `simpy.Environment()` and `simpy.Resource(capacity=SERVERS)` using FCFS.
2. Generate Poisson arrivals from time 0 until the end of the measured arrival window. An arrival at or after the end boundary is not admitted.
3. At each admitted arrival, sample independent exponential service at `service_rate` and patience at `PATIENCE_RATE` from the injected RNG.
4. Race the server request against the patience timeout. If the request wins, service starts; otherwise cancel/release the pending request correctly and record abandonment. A zero-probability exact tie is resolved in favor of a granted request.
5. Warm-up arrivals affect queue state and measured-window server utilization but are excluded from measured-customer response counts and waits.
6. A customer is measured when its arrival time is within `[measurement_start, measurement_end)`.
7. Stop generating arrivals at `measurement_end`, then drain all admitted customer processes to service completion or abandonment. Every measured customer must be accounted for.
8. `mean_wait_time` is the arithmetic mean time from measured arrival until service start or abandonment, over all measured customers; zero when none arrive.
9. `throughput = served_customers / run_duration` for the measured-arrival cohort.
10. `server_utilization` is total service-busy overlap with the measured window, including warm-up customers, divided by `SERVERS * run_duration` and safely clamped to `[0,1]` against floating-point boundary noise.

No event-level printing/logging is allowed.

### P3B1-R5 — Production result builder

Update canonical `result.py` to build the future production Result DB record from:

```python
build_result(scenario, metrics, effective_seed)
```

`scenario` retains the existing keys `parameterset_id`, `arrival_rate`, `service_rate`, `run_duration`, and `seed_policy`. `metrics` is the exact `model.run` dict. Output contains the existing scenario/effective-seed fields plus all exact model metric fields from P3B1-R3. It must reject missing/unexpected metric fields, non-finite floats, inconsistent accounting, alias mismatch, or probability/utilization outside `[0,1]` rather than persisting malformed data.

`RESULT_FIELDS` must enumerate the deterministic output field order. Do not add Result DB credentials or `scenario_id` to the JSON result.

Keep `seed.py` behavior unchanged in P3B1: production effective-seed derivation remains `seed_policy + hostname` until a separately approved ruling changes it.

### P3B1-R6 — Credential-free CLI

Add `cli.py` with required flags:

```text
--arrival-rate INTEGER
--service-rate INTEGER
--run-duration INTEGER
--seed INTEGER
```

The first three are positive integers; `seed` is a non-negative integer. The CLI constructs `random.Random(seed)`, runs one replication, and writes exactly one compact, key-sorted JSON object plus newline to stdout containing:

- `arrival_rate`, `service_rate`, `run_duration`, `effective_seed`;
- all exact model metric fields from P3B1-R3.

No credentials, database configuration, hostname derivation, NATS, or infrastructure input is accepted. Diagnostics go to stderr and invalid input exits non-zero. JSON serialization uses `allow_nan=False`; successful stdout contains no prose.

### P3B1-R7 — Deterministic tests

Replace/extend the canonical tests and add `test_cli.py`. Tests must cover:

- exact constants and result-key set;
- input validation, including boolean rejection;
- same inputs + same effective seed produce identical metrics and byte-identical CLI JSON;
- distinct seeds exercise distinct output over a fixed seed set without asserting every pair differs;
- accounting and compatibility alias invariants;
- finite values and probability/utilization bounds;
- zero measured arrivals produce finite zeros;
- warm-up customers affect state/utilization but not measured counts;
- drain accounts for measured customers whose outcome occurs after the measured arrival window;
- abandonment correctly removes pending requests without ghost capacity consumption;
- two-server concurrency is observed in a deterministic scripted-RNG case;
- strongly separated fixed-seed low/high arrival ensembles show increased waiting/abandonment;
- strongly separated fixed-seed low/high service ensembles show decreased waiting/abandonment;
- result builder success and every ruled malformed-result rejection;
- CLI invalid-input stderr/non-zero behavior and absence of credential/database options;
- no module-global `random` use in `model.py`.

Ensemble tests must use fixed seeds and robustly separated configurations, not probabilistic thresholds likely to flake.

### P3B1-R8 — Documentation and scope

Update `pads-paper/simulation-model/README.md` with:

- original-implementation statement;
- precise event/measurement semantics;
- constants and metric definitions;
- host CLI examples using placeholders only;
- distinction between the credential-free CLI, future standalone container, and future PostgreSQL production runner;
- reproducibility limitation: live production seeds remain hostname-derived, while CLI replay uses recorded effective seeds directly.

Do not modify:

- `pads-paper/simulation-model/container/**`;
- paper-local EDS, Translator, PPS, Detail DB, or Result DB;
- original `component-templates/**`;
- shared Operator, Scenario Manager, CRD, manifests, root Makefile, harness, or `go.work`.

## Observable acceptance

1. `cd pads-paper/simulation-model/src && python3 -m unittest -v` passes.
2. Run the CLI twice with identical arguments and prove byte-identical stdout; parse the JSON and verify exact fields and finite/bounded values.
3. Run an invalid CLI invocation and prove non-zero exit, stderr diagnostics, and empty stdout.
4. `rg`/AST inspection confirms `model.py` does not import or call module-global `random`, and no credential/database/network option exists in `cli.py`.
5. The implementation diff edits only `pads-paper/simulation-model/src/**` and `pads-paper/simulation-model/README.md`.
6. `git diff --check` passes.
7. Repository-root `make test-fast` passes. No cluster smoke is required because P3B1 creates no Dockerfile, image, manifest, reconciliation, API, or integration change.
8. The implementation commit carries the assigned orchestration task/dispatch trailer.

## Completion

P3B1 is `complete` only after independent Pi/GLM review returns `approve` against the implementation HEAD and the approved child branch is landed on `PADS-paper`.
