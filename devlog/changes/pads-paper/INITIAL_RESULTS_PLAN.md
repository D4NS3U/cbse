# Initial-results plan for the PADS paper

## Purpose

The first results should demonstrate both:

1. **scientifically recognizable model behavior** from an impatient multi-server queue; and
2. **CBSE orchestration behavior**: parameterized scenarios, isolated replications, persisted results, and adaptive additional-runner rounds until a confidence-interval precision target is met.

This is an initial-results plan, not a claim of completed measurements.

## Research questions

### RQ1 — Does the generated model exhibit expected queueing behavior?

As offered load `ρ = λ/(cμ)` approaches and exceeds 1, mean waiting time and abandonment probability should increase at the ensemble level. Increasing μ with λ fixed should reduce both responses.

### RQ2 — Can a recorded stochastic replication be replayed deterministically?

A model repetition is reproducible when its parameter set and recorded effective seed are identical. The current runner derives that seed from `seed_policy` and the runtime Pod hostname; therefore a second Kubernetes execution is not guaranteed to receive the same seed. The result row must retain the effective seed so the model can be replayed directly with `random.Random(effective_seed)`. Within one scenario run, effective seeds should be distinct.

### RQ3 — Does precision-driven post-processing allocate more replications where uncertainty is higher?

Scenarios near the capacity boundary should generally need more replications to meet the same absolute half-width ε than clearly underloaded scenarios. Across repeated or common-sequence comparisons, tightening ε should generally increase the required replication count; one pair of independent adaptive runs is not a monotonicity proof.

### RQ4 — What orchestration cost does CBSE add?

Report end-to-end scenario duration, runner rounds, runner count, and infrastructure timing separately from simulated time. Do not mix wall-clock orchestration latency with the model's simulation-time units.

## Fixed model configuration

| Item | Value |
|---|---:|
| Model | `erlang_a_v1` |
| Servers `c` | 2 |
| Patience rate `θ` | 1.0 (mean patience: one simulation time unit) |
| Warm-up | `max(10, 0.10 × T)` |
| Service discipline | FCFS through `simpy.Resource` |
| SimPy | 4.1.2, already pinned by the runner base |
| Primary response | `mean_wait_time` |
| Confidence level | 95%, existing Student-t PPS policy |

## Experiment A — load/service response surface

Use a compact full factorial over arrival and service rates. Keep duration and seed policy fixed.

| Factor | Levels |
|---|---|
| `arrival_rate` λ | 4, 6, 8, 10 |
| `service_rate` μ | 4, 5 |
| `run_duration` T | 500 |
| `seed_policy` | 2027 |

With `c=2`, the eight offered-load points are:

| λ | μ | ρ = λ/(2μ) |
|---:|---:|---:|
| 4 | 4 | 0.50 |
| 6 | 4 | 0.75 |
| 8 | 4 | 1.00 |
| 10 | 4 | 1.25 |
| 4 | 5 | 0.40 |
| 6 | 5 | 0.60 |
| 8 | 5 | 0.80 |
| 10 | 5 | 1.00 |

Scenario settings:

- initial `number_of_reps`: 20;
- `confidence_metric` ε: 0.05 simulation-time units;
- PPS policy: `statistical`;
- provisional `max_replications`: 500;
- provisional `max_runners_per_round`: 50 to bound operational bursts without changing the scientific cap.

The EDS creates four arrival entries, two service entries, one duration entry, one seed entry, eight recipes, and eight scenarios. This exercises its existing entry-sharing and recipe composition without any EDS code change.

### Expected figures

1. Mean waiting time with 95% CI versus ρ, grouped by μ.
2. Abandonment probability versus ρ.
3. Throughput and server utilization versus ρ.
4. Total replications and runner rounds required to meet ε for each scenario.

Expected behavior is an ensemble trend, not strict monotonicity in every finite sample.

## Experiment B — precision sensitivity

Choose the boundary case `λ=8`, `μ=4`, `T=500`, `seed_policy=2027` (`ρ=1`) and create separate scenarios with the same recipe but different precision targets:

| Scenario | Initial reps | ε |
|---|---:|---:|
| B1 | 10 | 0.20 |
| B2 | 10 | 0.10 |
| B3 | 10 | 0.05 |

Report:

- final sample mean and half-width;
- total replications;
- number and size of additional-runner rounds;
- end-to-end wall-clock duration;
- whether the scenario stopped `met` or `stop_unmet`.

The three live scenarios use independently derived hostname-based seed sets, so strict monotonic replication counts are not a valid per-run invariant: sampling variation can occasionally let a tighter target stop earlier. Report the observed counts without hiding such cases. For a paired precision illustration, use one recorded ordered sample sequence offline and calculate the first prefix satisfying each ε; only that common-sequence analysis has the expected nondecreasing sample-count relation as ε tightens.

## Experiment C — recorded-seed replay check

Select result rows from Experiment A and replay each model invocation outside Kubernetes using the recorded parameters and `random.Random(effective_seed)`. Compare the reconstructed result with the persisted row.

Required equality:

- model inputs and `model_id`;
- effective seed;
- served/abandoned/total counts;
- mean wait, abandonment probability, throughput, and utilization.

This is a model-level reproducibility check, not an independence test. Also verify that effective seeds are distinct within each live scenario. Exact whole-experiment replay is not promised by the current hostname-derived seed contract and must be stated as a limitation unless that contract is separately redesigned.

## Measurements to retain

### Model-level measurements

- `mean_wait_time`;
- `served_customers`;
- `abandoned_customers`;
- `total_customers`;
- `abandonment_probability`;
- `throughput`;
- `server_utilization`;
- effective seed and model version.

### CBSE-level measurements

- scenario creation and terminal timestamps;
- runner round and round size;
- total runner Jobs/Pods and successful completions;
- translation attempt count and generated image digest;
- evaluation attempts, final sample mean, half-width, and verdict;
- wall-clock time by stage where available;
- immutable component and generated-runner image digests.

Do not put credentials, private registry names, cluster endpoints, kubeconfig paths, or Secret payloads into paper artifacts.

## Statistical presentation

- Use the PPS's Student-t 95% confidence interval for `mean_wait_time` and state that ε is an **absolute half-width target**, not a confidence level.
- For abandonment probability, report the mean and an interval across independent replications; choose and document the interval method before final analysis.
- Show individual replication points or distributions where space permits; do not report only aggregate means.
- Distinguish adaptive stopping results from a fixed-sample comparison. Adaptive sample sizes can complicate naive between-scenario inference.
- Treat the first results as a system demonstration, not calibrated evidence about a real call center.

## Reproducibility bundle

The paper-result run should preserve, outside tracked source where operational/private:

- branch commit and dirty-tree status;
- public source/citation links from `MODEL_SELECTION.md`;
- digest-pinned component and generated-runner images;
- EDS design export or exact API request sequence;
- parameter recipes and scenario settings;
- result rows with credentials removed;
- PPS verdict history and replication rounds;
- JUnit/test receipts for the implementation commit;
- plotting/analysis script and its dependency lock;
- sanitized cluster/software version summary.

Tracked documentation should use placeholders for operational infrastructure. Private image-value files and raw cluster diagnostics remain ignored artifacts.

## Acceptance before using results in the paper

- model unit/invariant tests pass;
- `make test-fast` passes;
- full smoke passes with the new model and richer result assertions;
- a manual retained run confirms EDS design, recipes, PPS adaptive rounds, and final result rows;
- recorded-effective-seed model replay succeeds;
- no missing/malformed PPS observations;
- every plotted point traces to a scenario ID, parameterset ID, commit, and immutable image digest;
- limitations are stated alongside the figures.
