# PADS paper — initial-results simulation model

**Status: DOCUMENTATION DRAFT — branch `PADS-paper`; model selected, implementation not started.**

## Mission

Replace the current minimal single-server queue used by the reference Translator with a small but research-recognizable discrete-event simulation for the first PADS paper results. The model must be understandable, reproducible, published in a reference implementation, and parameterizable through CBSE's existing four-value experiment-design contract so the Experimental Design Service (EDS) does not require a redesign.

The selected model is an **impatient-customer multi-server queue**, specifically a finite-horizon **M/M/c+M (Erlang-A / Palm queue)**. It generalizes SimPy's published **Bank Renege** example from one counter with reneging customers to a fixed number of parallel servers and exponentially distributed patience.

Detailed rationale and sources: [MODEL_SELECTION.md](MODEL_SELECTION.md).

Initial paper experiment and figure plan: [INITIAL_RESULTS_PLAN.md](INITIAL_RESULTS_PLAN.md).

## Scope

### In scope

- replace the generated runner's current M/M/1 model with `erlang_a_v1` on the `PADS-paper` branch;
- retain the existing EDS four-parameter design and recipe contract;
- retain `mean_wait_time` as the PPS primary metric and add richer result fields;
- add deterministic, invariant, integration, and live acceptance tests;
- define reproducible initial-result experiments and sanitized paper artifacts.

### Out of scope

- generic/dynamic EDS parameter schemas;
- a second model family or runtime model selector;
- CRD, Scenario Manager wire/state-machine, or PPS stopping-policy changes;
- production calibration against a real call center;
- infrastructure benchmarks presented as model-time measurements.

### Compatibility and breaking semantics

The API, database schema, recipe wire, and primary PPS metric remain compatible. The scientific meaning of the generated runner changes from the current single-server queue to `erlang_a_v1`; result rows therefore gain a required `model_id`, and the old and new model outputs must not be pooled as if they came from one model version.

Kubernetes compatibility remains the repository baseline (Kubernetes ≥ 1.30, alpha4 only). No feature gate or cluster-wide setting is changed by this work.

## Workflow decisions


| Concern | Decision |
|---|---|
| DES problem | M/M/c+M queue with customer abandonment |
| Published implementation anchor | SimPy `Bank Renege`, published in the SimPy source repository and historical GitHub mirror |
| Academic anchor | Garnett, Mandelbaum, and Reiman (2002), *Designing a Call Center with Impatient Customers* |
| EDS impact | **No EDS API, UI, recipe, or database-schema change** |
| Existing parameter mapping | `arrival_rate = λ`, `service_rate = μ` per server, `run_duration = T`, `seed_policy = deterministic seed base` |
| Primary PPS metric | Preserve `mean_wait_time` and its existing fixed-width 95% confidence-interval stopping rule |
| Model complexity | Parallel resources, queueing, abandonment through condition events, warm-up, drain, and multiple response metrics |
| First implementation scope | Translator-generated runner model/result contract and tests; paper-specific fixtures and documentation |

## Why this model fits CBSE

The current EDS and Scenario Detail Database already expose exactly four integer parameters: the first three are positive, while `seed_policy` is non-negative.

1. `arrival_rate`
2. `service_rate`
3. `run_duration`
4. `seed_policy`

Those names and domains are natural inputs for a queueing model. The proposed model therefore needs no new EDS endpoint, no generic parameter editor, no new recipe wire shape, and no Detail DB migration. `recipe_info` remains `{ "parameterset_id": N }`, and the Translator performs the same lookup.

The PostProcessingService (PPS) already evaluates one `mean_wait_time` observation per replication and requests additional runners until the absolute 95% confidence-interval half-width is at most the scenario's `confidence_metric`. The new model preserves that primary field, so the adaptive-replication loop remains unchanged.

Compared with the current M/M/1 example, the proposed model adds:

- `c = 2` parallel servers via `simpy.Resource`;
- customer abandonment by racing a resource request against a patience timeout;
- an exponential patience distribution;
- a fixed warm-up interval to reduce empty-system initialization bias;
- a drain phase after arrivals stop;
- primary and secondary response metrics suitable for paper figures.

This is enough sophistication to demonstrate nontrivial DES behavior without introducing a large model-specific configuration surface.

## Design patterns (Gang of Four)

| Concern | Pattern | Concrete application | Why direct composition is insufficient |
|---|---|---|---|
| Assemble a complete, immutable generated-runner build context from the resolved recipe | Builder | The existing Translator generator builds `scenario.json`, result DB configuration, Python modules, and the generated Dockerfile as one validated output; the PADS model extends that builder without leaking partial contexts | Generation has multiple required files and security checks; partial output must never be published |
| Keep model-specific simulation logic isolated from Translator orchestration and storage clients | Adapter | The existing generator boundary adapts the Detail DB's `simulation_parameters` row into the runner's model/result modules; `model.run(...)` stays a pure model-facing API over SimPy and an injected RNG | Directly mixing DB lookup, BuildKit publication, and event logic would prevent independent scientific tests |

No additional pattern is licensed. In particular, a runtime Strategy registry for multiple models is out of scope because this branch ships one model version.

## Global constraints

- Obey root `AGENTS.md`; Go/test-harness/Dockerfile changes require `make test-fast`, and image/manifest/integration changes require the repository smoke tier.
- Specifications and citations are read-only inputs for implementation workers.
- Keep the EDS parameter names/domains, recipe schema, Detail DB view, Scenario Manager messages, and PPS `mean_wait_time` contract unchanged.
- Use the runner's injected local RNG only; never use Python module-global random state.
- One replication writes one finite JSON result row or exits non-zero; no partial/NaN/Inf results.
- No event-level logs, credentials, private registry values, endpoints, or local paths in tracked artifacts.
- Every scientific constant is recorded in `model_id` documentation; changing constants requires a new model version.
- Immutable image digests, numeric non-root identity, and current runner security contracts remain mandatory.
- Do not weaken existing tests to accommodate changed semantics; update assertions explicitly and retain compatibility fields where ruled.

## Proposed model contract (`erlang_a_v1`)

### Experimental parameters

| Existing CBSE field | Model meaning | Domain |
|---|---|---|
| `arrival_rate` | Poisson arrival rate λ per simulation time unit | integer > 0 |
| `service_rate` | Exponential service rate μ **per server** per time unit | integer > 0 |
| `run_duration` | Length T of the measured arrival window | integer > 0 |
| `seed_policy` | Base input to the existing per-repetition effective-seed derivation | integer ≥ 0 |

### Fixed, versioned model constants

The first paper model intentionally does not expose these through EDS:

| Constant | Initial value | Reason |
|---|---:|---|
| `model_id` | `erlang_a_v1` | Makes the scientific implementation explicit in every result row |
| `servers` (`c`) | 2 | Small enough to understand; creates a real multi-server capacity boundary |
| `patience_rate` (`θ`) | 1.0 | Holds customer patience fixed while λ and μ vary; mean patience is one simulation time unit |
| `warmup_fraction` | 0.10 | Reduces initial empty-system bias without adding an EDS parameter |
| `minimum_warmup` | 10 time units | Avoids a negligible warm-up for short runs |

Changing one of these constants creates a new model version rather than silently changing `erlang_a_v1`.

### Replication algorithm

For each runner repetition:

1. Derive the effective seed using the existing runner seed contract.
2. Create a SimPy environment and a `Resource(capacity=2)`.
3. Generate arrivals with exponential interarrival times at rate λ.
4. Give every customer an exponential service requirement at rate μ.
5. Give every customer exponential patience at fixed rate `θ = 1.0`.
6. Race `server.request()` against `env.timeout(patience)`:
   - request wins → customer enters service;
   - timeout wins → customer abandons.
7. Run an unmeasured warm-up of `max(10, 0.10 × T)` time units.
8. Measure customers arriving during the following T time units.
9. Stop generating arrivals at the end of the measured window and drain all admitted customers to service completion or abandonment.
10. Persist one JSONB result row.

No per-event logging is allowed; output volume remains proportional to replications, not DES events.

### Result contract

Preserve the fields currently consumed by CBSE and add paper metrics:

| Field | Meaning |
|---|---|
| `parameterset_id` | Existing recipe locator |
| `arrival_rate`, `service_rate`, `run_duration`, `seed_policy` | Baked scenario parameters |
| `effective_seed` | Existing per-repetition deterministic seed |
| `model_id` | `erlang_a_v1` |
| `completed_customers` | Compatibility field; equal to `served_customers` |
| `mean_wait_time` | **Primary PPS metric:** mean time from arrival until service start or abandonment, over measured customers |
| `served_customers` | Measured customers that entered and completed service |
| `abandoned_customers` | Measured customers whose patience expired first |
| `total_customers` | `served_customers + abandoned_customers` |
| `abandonment_probability` | `abandoned_customers / total_customers`, or 0 for an empty measured set |
| `throughput` | `served_customers / run_duration` |
| `server_utilization` | Aggregate measured busy-server time divided by `servers × run_duration`, clamped to `[0,1]` |

The PPS continues reading only `mean_wait_time`; additive JSON fields do not require a Result DB schema change.

## Scope for the later implementation

Expected implementation surfaces:

- `component-templates/translator/internal/generator/runnermod/model.py`
- `component-templates/translator/internal/generator/runnermod/result.py`
- associated generator/model/result tests and golden fixtures
- Translator README/model documentation
- paper-specific e2e or acceptance fixtures that assert the richer result fields

Expected unchanged surfaces:

- EDS API/UI and its four parameter names
- Scenario Detail Database schema and recipes
- Scenario Manager wire and state machine
- PPS wire and statistical stopping algorithm
- alpha4 CRD

Any discovered need to modify those unchanged surfaces is a design contradiction and requires a new documented decision before implementation.

## Scientific validity checks

The implementation must have deterministic tests for:

- same parameters + same effective seed → byte-equivalent result record;
- low load `λ << cμ` → low waiting and low abandonment over a fixed seed ensemble;
- strongly separated low- and high-λ fixed-seed ensembles show the expected increase in waiting/abandonment;
- strongly separated low- and high-μ fixed-seed ensembles show the expected decrease in waiting/abandonment;
- accounting identity `served + abandoned = total`;
- utilization and abandonment probability remain in `[0,1]`;
- zero measured arrivals produce finite zero-valued aggregate metrics;
- event draining leaves no measured customer unaccounted for;
- no module-global random state.

These are ensemble/invariant tests, not claims that every individual random replication is monotonic.

## Out of scope for the first results

- exposing server count, patience rate, warm-up policy, or service discipline in EDS;
- priority queues, multiple customer classes, skills-based routing, retrials, or time-varying arrivals;
- importing a large external simulation project or its dependency graph;
- replacing SimPy or the existing runner-base image;
- changing the PPS's primary metric or confidence-interval policy;
- claiming production call-center calibration from synthetic parameters.

## Implementation slices

| Slice | Dependency | Result | State |
|---|---|---|---|
| P1 — model selection and initial-results documentation | none | Selected model, citations, parameter/result contract, experiment plan | **complete** |
| P2 — `erlang_a_v1` runner model | P1 | Pure SimPy model with injected RNG, warm-up/drain, abandonment, accounting/utilization metrics, unit/invariant tests | incomplete |
| P3 — generated result and Translator integration | P2 | Additive result contract, generated-runner fixtures/goldens, README updates, PPS compatibility proof | incomplete |
| P4 — end-to-end acceptance | P3 | Fresh immutable images, EDS-driven scenarios, richer result assertions, adaptive PPS rounds, recorded-effective-seed replay, smoke evidence | incomplete |
| P5 — paper-result bundle | P4 | Sanitized experiment inputs/results, analysis/plots, provenance manifest, limitations | incomplete |

Detailed worker slices and dispatch prompts will be authored only after the user accepts this documentation draft.

## Verification gates

- Documentation gate: every source link resolves; terminology distinguishes the upstream uniform-patience Bank Renege example from the CBSE exponential-patience Erlang-A generalization.
- Model gate: deterministic unit/invariant tests, including accounting, bounds, seed replay, ensemble trend checks, and no global RNG.
- Repository gate: `make test-fast` after every implementation slice that changes Python templates, Go generator code, Dockerfiles, or harness tests.
- Cluster gate: `make test-smoke` with freshly built immutable Translator/runner inputs after integration changes; explicit kubeconfig and dedicated harness namespace only.
- Manual research gate: retained EDS-driven run proving recipes, adaptive PPS rounds, result rows, distinct live effective seeds, and model-level replay from recorded effective seeds before results are used in the paper.
- Reproducibility gate: every plotted observation maps to branch commit, parameterset/scenario, effective seed, and immutable image digest.

Forbidden shortcuts: no tag images, no skipped/weakened tests, no hand-edited result rows, no private operational values in tracked files, and no claim that planned experiments are completed results.

## Completion and handoff

Slice vocabulary is `complete`, `incomplete`, or `verification-blocked`. A slice is complete only with its named evidence. Implementation workers commit only to dedicated child branches with orchestration trailers; independent review is required before landing on `PADS-paper`.

All implementation and paper-result work remains on `PADS-paper` or dedicated child branches derived from it. Nothing merges to `main` without explicit user approval. Documentation precedes implementation; this draft records the selected model and experiment plan but does not claim completed results.

When implementation begins, create `IMPLEMENTATION_HANDOFF.md` to record per-slice task/dispatch IDs, reviewed HEADs, tests, smoke/acceptance runs, and resume notes. Operational evidence stays under ignored `artifacts/` paths.
