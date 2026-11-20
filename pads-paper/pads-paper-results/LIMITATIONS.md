# PADS initial results — limitations

Companion to the campaign evidence in `pads-paper/results/evidence/`
and the figures rendered by `analysis/plot-figures.py`. Per the P6
slice (P6-R4), limitations are stated alongside the figures.

## Seeds and replay scope

**Hostname-derived production seeds.** The runner derives each
replication's effective seed from the `seed_policy` and the runtime Pod
hostname. An exact *whole-experiment* replay (re-running the campaign
and expecting the same effective seeds) is therefore **not promised**
by the current seed contract; that contract would have to be
separately redesigned for such a guarantee. What *is* promised — and
what the committed evidence demonstrates (Experiment C,
`replay-seeds.py`) — is **model-level replay**: given a recorded
effective seed, the canonical model re-runs to bit-identical metrics
under SimPy 4.1.2 with `random.Random(effective_seed)`. Within one live
scenario the recorded effective seeds are verified distinct
(`assert-results.py`).

## Single campaign run

**One run, one cluster.** The bundle records a single campaign
execution on one dedicated test cluster. There is no second campaign
run, so run-to-run variability of the *orchestration* layer (adaptive
round lengths, stage durations, runner scheduling) is not measured —
only the structure of one run is recorded. Statistical statements about
the model responses rest on within-scenario replications, not on
repeated campaigns.

## Adaptive stopping and between-scenario inference

**Adaptive stopping complicates naive between-scenario inference.**
Each scenario stops independently when its Student-t 95% half-width
reaches `confidence_metric` ε (or records `stop_unmet`). Final sample
sizes therefore differ across scenarios for the *same* ε, and the
adaptive loop makes each scenario's effective replication count a
random stopping time. Comparing ensemble means across scenarios is
valid as a descriptive surface (the figure grids are drawn from the
recorded means), but the bundle makes no inferential claim that would
be invalid under unequal sample sizes or optional-stopping effects.
Tightening ε and observing more required replications is reported as
the Experiment-B illustration with the plan's order caveat: it is a
common-sequence precision illustration, not a monotonicity proof
(`INITIAL_RESULTS_PLAN.md`, RQ3).

## Ensemble trends, not per-replication monotonicity

**Monotonicity claims are at the ensemble level only.** The expected
queueing behavior — increasing mean wait and abandonment as offered
load `ρ = λ/(cμ)` approaches and exceeds 1, and reduced responses when
μ increases with λ fixed — is an ensemble-trend statement over the
per-scenario sample means. Individual replications are not monotone in
ρ and are not required to be; the figures plot replication means with
95% CI error bars, and overlapping intervals across adjacent ρ values
are reported as-is.

## `stop_unmet` interpretation

**`stop_unmet` is recorded, not censored.** A scenario that reaches
the provisional replication cap with the half-width still above ε is
reported as `stop_unmet` with its final statistics. It is *not*
excluded from the surface plots or aggregates, and its presence would
be a result in its own right (a precision target the adaptive loop did
not meet), not a failure of the run.

## Synthetic parameters

**No real call-center calibration.** All rates (λ ∈ {4,6,8,10},
μ ∈ {4,5}, T = 500, seed policy 2027) are synthetic planning values
chosen to span `ρ ∈ [0.4, 1.25]` around the nominal `c=2`
service-capacity boundary. The bundle makes no claim that the
parameters match any real call-center system, and Whitt's Erlang-A
sensitivity analysis motivates the factor structure without licensing
its large-system results as quantitative predictions for this
finite-horizon two-server simulation.

## Fixed model constants

**Warm-up and patience are fixed.** Warm-up discards
`max(10, 0.10 × T)` = 50 simulation time units of every replication,
and the patience rate is `θ = 1.0` (mean patience one simulation time
unit). Neither was varied in the campaign; responses are specific to
these constants.

## Analysis environment

**The analysis environment is recorded, not assumed.** Statistics are
pure stdlib (Python version recorded in the campaign `run.md` at
settlement). Plotting uses the pinned environment
(`analysis/requirements-analysis.txt`: `matplotlib==3.11.2`,
`numpy==2.5.4`); the SVG figures embed the matplotlib version in their
metadata, so figure bytes are reproducible only from that pinned
environment. The Experiment-C replay requires SimPy **4.1.2** exactly
(the model's contract); the recorded replay report names the versions
used.

## P5-noted accepted omissions (carried into P6)

- The P5 acceptance evidence
  (`pads-paper/acceptance/evidence/pads-20261105154051-121fcd/`) is a
  **single-scenario** acceptance run (one scenario, five replications),
  not a factorial experiment; it proves the chain end to end, nothing
  statistical. The campaign here is the first factorial run.
- Per P6-R6, the unchanged 15-spec smoke suite is **not re-run** at P6
  settlement: no shared code, manifest, or harness-built image changed
  since the same-settlement-day receipt
  `20261105152950-6d0ee4` (15/15) over the identical digest set. The
  campaign `run.md` references that receipt instead.
- The paper-local Translator image is **reused unchanged** from the P5
  publication (no new image publication, because no image content
  changed); its digest is recorded in the campaign `run.md`.
