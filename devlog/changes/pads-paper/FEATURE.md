# PADS paper — initial-results simulation model

**Status: DOCUMENTATION DRAFT — branch `PADS-paper`; model selected, implementation not started.**

## Mission

Replace the current minimal single-server queue used by the reference Translator with a small but research-recognizable discrete-event simulation for the first PADS paper results. The model must be understandable, reproducible, published in a reference implementation, and parameterizable through CBSE's existing four-value experiment-design contract so the Experimental Design Service (EDS) does not require a redesign.

The selected model is an **impatient-customer multi-server queue**, specifically a finite-horizon **M/M/c+M (Erlang-A / Palm queue)**. It generalizes SimPy's published **Bank Renege** example from one counter with reneging customers to a fixed number of parallel servers and exponentially distributed patience.

Detailed rationale and sources: [MODEL_SELECTION.md](MODEL_SELECTION.md).

Initial paper experiment and figure plan: [INITIAL_RESULTS_PLAN.md](INITIAL_RESULTS_PLAN.md).

## Scope

### In scope

- create one repository-root `pads-paper/` source tree that contains every paper-specific component implementation and its local documentation;
- retain the existing `component-templates/**` implementations as the reusable baseline and extend copied paper-local variants rather than rewriting components from scratch;
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
| Paper-specific source root | All paper-owned code lives under repository-root `pads-paper/`; existing shared templates remain unchanged baselines |
| Template reuse | Copy the relevant existing template into its paper-local `component/` directory, preserve its contracts/tests, and add only Erlang-A-required changes; clean rewrites are forbidden |
| Canonical model source | `pads-paper/simulation-model/src/` owns the original CBSE Erlang-A Python implementation; no third-party GitHub model source is copied |
| Translator synchronization | The paper-local Translator receives a synchronized byte-identical copy of the canonical production model modules; an automated parity test prevents drift |
| Standalone model container | Credential-free CLI image accepts scenario parameters plus an effective seed, runs one replication, and emits exactly one JSON object to stdout |
| Production runner | The paper-local Translator parameterizes the synchronized model for one scenario, and its existing BuildKit sidecar builds and pushes an executable digest-resolved runner image; that runner writes one result to PostgreSQL |
| Paper component set | SimPy model/container, EDS, Translator, PPS, Scenario Detail Database, and Result Database |
| Shared framework | Experiment Operator and Scenario Manager remain shared and unchanged unless a later, explicitly approved contradiction proves a paper-local change necessary |
| Result DB baseline | No repository-owned Result DB template currently exists; preserve the existing official-PostgreSQL/no-init-schema contract and add only paper-local packaging or documentation actually required |
| First implementation scope | Establish the paper source tree and provenance first; model and component behavior follow in ordered slices |

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
- Put all paper-specific implementation files below `pads-paper/`; do not modify `component-templates/**` to implement the paper model.
- **Template-reuse ruling:** every paper-local EDS, Translator, PPS, and Scenario Detail Database starts from the corresponding existing `component-templates/**` implementation and extends it only with behavior necessary for `erlang_a_v1`. Preserve existing tests, security contracts, health/readiness behavior, database wiring, and container construction unless this specification explicitly changes them. No clean-room replacement, parallel framework, or speculative abstraction is allowed.
- The paper-local simulation model starts from the existing Translator `runnermod` API/security contracts and `runner-base` container contract, but the Erlang-A event logic is an original CBSE implementation: do not copy third-party GitHub model source.
- `pads-paper/simulation-model/src/` is canonical. A later Translator integration may copy only the ruled production modules and must enforce byte parity automatically; independent hand-maintained model forks are forbidden.
- The standalone model image is credential-free and writes exactly one finite JSON result to stdout. The Translator-generated production runner separately owns PostgreSQL Result Database insertion.
- Preserve the existing Translator/BuildKit/registry flow: parameter lookup and baking, sidecar build, authenticated push, digest resolution/verification, and ready-message publication.
- The Result Database starts from the existing official-PostgreSQL/no-repository-init contract because no standalone repository Result DB template exists.
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

## Paper-specific source tree

The implementation root is:

```text
pads-paper/
├── README.md
├── simulation-model/
│   ├── README.md
│   ├── src/                    # canonical model source
│   └── container/
│       └── base/               # imported runner-base contract
├── experimental-design-service/
│   └── component/              # imported and later extended template
├── translator/
│   └── component/
├── post-processing-service/
│   └── component/
├── scenario-detail-database/
│   └── component/
└── result-database/
```

P2 created the ownership boundary and per-directory provenance documentation. P3A imports the baselines into the shown implementation directories without modifying their bytes; P3B then replaces only the canonical model behavior and adds the standalone CLI image. Later integration copies the ruled canonical production modules into the paper-local Translator and enforces parity.

Baselines:

| Paper directory | Required baseline |
|---|---|
| `simulation-model/` | `component-templates/translator/internal/generator/runnermod/` plus `component-templates/translator/runner-base/` |
| `experimental-design-service/` | `component-templates/experimental-design-service/` |
| `translator/` | `component-templates/translator/` |
| `post-processing-service/` | `component-templates/post-processing-service/` |
| `scenario-detail-database/` | `component-templates/scenario-detail-database/` |
| `result-database/` | Existing official PostgreSQL image contract: no repository initialization SQL; runner-created `scenario_<id>_results` tables |

Expected paper-local implementation surfaces include the copied Translator runner model/result contract, generator/model/result tests and fixtures, component READMEs, database packaging, and paper-specific e2e acceptance. The original templates remain unchanged so the repository retains its general reference implementation.

Expected unchanged shared surfaces are the Experiment Operator, Scenario Manager, alpha4 CRD, EDS parameter/wire contract, Detail DB view shape, and PPS statistical stopping algorithm. Any discovered need to modify these surfaces is a design contradiction and requires a new documented user decision before implementation.

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
| P2 — paper component workspace foundation | P1 | `pads-paper/` ownership tree, component placeholders, baseline provenance, and explicit template-reuse instructions | **complete — approved and landed at `1d5ad77`** |
| P3A — exact template import | P2 | Paper-local `component/` copies and simulation-model baseline import, with source/destination byte-parity evidence and no behavioral changes | **complete — approved and landed at `2b3fdb4`** |
| P3B1 — canonical `erlang_a_v1` model and CLI | P3A | Original pure SimPy implementation, deterministic credential-free host CLI, warm-up/drain, abandonment, accounting/utilization metrics, and unit/invariant tests | **complete — approved and landed at `dbadcd8`** |
| P3B2 — standalone model container | P3B1 | Immutable-base credential-free application image for the accepted CLI; local build/run and mandatory image/smoke verification | incomplete |
| P4 — Translator synchronization and component integration | P3B2 | Automated canonical-model parity, scenario parameterization, generated production runner/result contract, EDS/Detail DB/PPS compatibility, fixtures/goldens, and component-local tests | incomplete |
| P5 — end-to-end acceptance | P4 | Fresh immutable images, EDS-driven scenarios, richer result assertions, adaptive PPS rounds, recorded-effective-seed replay, and smoke evidence | incomplete |
| P6 — paper-result bundle | P5 | Sanitized experiment inputs/results, analysis/plots, provenance manifest, and limitations | incomplete |

P2 has a dedicated normative slice and Pi-worker dispatch prompt. Later slice specifications are authored only after the preceding boundary is settled.

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
