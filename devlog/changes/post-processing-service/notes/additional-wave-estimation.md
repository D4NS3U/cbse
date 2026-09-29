# Design note — The additional-wave estimator: sequential fixed-width precision control

**Component:** reference PostProcessingService (PPS), `component-templates/post-processing-service/`
**Provenance of the criterion:** the precision-based stopping criterion (half-width of a confidence interval for a selected performance metric vs a user-defined threshold ε, with a maximum-replications secondary criterion) originates from the author's own work on the CBSE methodology; this note documents the estimator that operationalizes it in the reference component.
**Normative context:** `FEATURE.md` decisions D6/D7, rulings Q4/Q6 (this directory). Landed and hand-vector-tested in the reference module (`internal/evaluation`).

## 1. The stopping criterion

After each replication wave, the PPS pools all completed replications of a scenario (one KPI observation per replication; the reference KPI is the model's `mean_wait_time`), updates the sample mean X̄ and sample variance s², and computes the half-width of a 95% confidence interval for the mean,

  h = t₀.₉₇₅,ₙ₋₁ · s / √n,

where n is the number of pooled observations and t₀.₉₇₅,ₙ₋₁ the Student-t quantile at n−1 degrees of freedom. The scenario is **met** when h ≤ ε, where ε is the user-supplied required precision carried per scenario (the `confidence_metric` of the experimental-design batch). Two secondary rules complete the criterion: a degenerate case (n ≥ 2 with s = 0 yields h = 0, hence met), and a maximum-replications bound: if the total number of replications reaches the user-configured `MAX_REPLICATIONS` with h > ε, the verdict is **stop-unmet** — the scenario fails the precision requirement with **all results preserved**, which is the second stopping criterion.

## 2. The additional-wave estimator

When the criterion is not met, the PPS must answer: *how many additional replications are needed?* The reference implements the standard sequential fixed-width estimate, obtained by inverting the half-width condition for n:

  n_req = ⌈ ( t₀.₉₇₅,ₙ₋₁ · s / ε )² ⌉,

i.e. the smallest integer replication count for which the projected half-width would satisfy the threshold, given the current variance estimate. The requested wave size is

  additional = clamp( n_req − n ),

clamped from below by the minimum batch (max(1, min(2, MAX_RUNNERS_PER_ROUND)) — a wave must make the estimator *computable*: from n = 1 no variance exists, so the degenerate rule requests the minimum batch) and from above by the per-wave safety bound MAX_RUNNERS_PER_ROUND and the remaining headroom to MAX_REPLICATIONS.

## 3. Small-sample behavior of the estimator

The estimator is conservative by construction, and its conservatism is strongest exactly where the sample is smallest. Two effects compound for small n:

1. **The t-quantile inflates.** t₀.₉₇₅,₂ = 4.303 (n = 3) versus ≈ 2.0 for n ≈ 30 — the interval honestly widens because little is known about the variance.
2. **The variance estimate itself is unstable.** The sample variance of three observations has a wide sampling distribution; an unlucky draw can place ŝ two to three times above the true σ.

Because the formula squares both factors, the requested wave size varies over more than an order of magnitude across runs of the same scenario. Illustrative spread (ε = 0.92, true s ≈ 0.7, evaluated at n = 3):

| ŝ from three draws | lucky (0.4) | typical (0.7) | unlucky (2.1) |
|---|---|---|---|
| t₀.₉₇₅(2) | 4.303 | 4.303 | 4.303 |
| **n_req** | **4** | **11** | **91** |

This variability is a **property, not a defect**: the procedure demands more compute precisely when its knowledge is weakest. A constant wave size (e.g. a fixed "5 more") would be stable but blind — it under-supplies when the variance is genuinely large (prolonging the loop) and wastes runners when few are needed. The fixed-width estimate is knowledgeable: it adapts to ŝ and ε.

## 4. Self-correcting convergence

The procedure is self-correcting across waves. Each wave's replications pool with all previous ones, so ŝ stabilizes, the t-quantile shrinks with growing n, and successive wave requests converge toward the true requirement. An overshooting wave costs compute but never correctness: the next evaluation simply observes a larger, more stable sample. Conversely, an undershooting wave is detected immediately (h still > ε) and topped up. The loop terminates either with h ≤ ε (met) or at MAX_REPLICATIONS (stop-unmet, results preserved) — both outcomes are safe.

## 5. Operational bounding: MAX_RUNNERS_PER_ROUND

Because small-n wave requests can be large, the per-wave bound MAX_RUNNERS_PER_ROUND exists as an operational pacing clamp. Its semantics (ruling Q7, 2026-09-29): a positive value caps each wave at that size; **0 explicitly disables the pacing** — waves are then sized by the estimate alone, still bounded by the maximum-replications headroom, and the disabled state is logged at startup; negative values are rejected at startup. The production default is 1000 (bounded defaults), and deployment-specific values are set per experiment via the PPS container args. The clamp is convergence-preserving: a bound wave simply triggers one further evaluation on a larger pooled sample, so the loop still terminates; at most the number of waves increases. Pacing is a resource-management decision, never a statistical one — ε and the criterion are untouched, and the *scientific* MAX_REPLICATIONS stopping criterion remains mandatory in all modes.

**Application in the reference smoke profile** (rulings Q4/Q6): determinism is achieved through *experimental design* — fleet sizes, never through threshold manipulation. The batch carries one well-powered scenario (n₀ = 30, expected to satisfy the criterion on the first wave with a measurable margin) and three underpowered scenarios (n₀ = 1 each; h is undefined below two observations, so a **second wave is guaranteed by construction** — the natural top-up the criterion describes). The smoke experiment sets MAX_RUNNERS_PER_ROUND = 30 so that wave sizes stay bounded (unlucky ŝ at n = 3 cannot translate into an unbounded pod burst); the per-scenario ε values remain the realistic precision demands supplied by the experimental-design stage.

## 6. Research outlook

The estimator as landed is the textbook baseline. Known refinements, in increasing order of novelty:

- **First-stage minimums:** do not trust ŝ below m₀ ≈ 10–20 pooled observations; fall back to conservative wave requests until the first-stage sample is reached (standard sequential-simulation practice).
- **Iterative quantile updates:** recompute n_req with the t-quantile of the *projected* n rather than the current n (a fixed-point iteration on the quantile).
- **Variance stabilization:** transform skewed KPI observations (queueing-time distributions are right-skewed) before interval construction.
- **Batch-size policies:** cost-aware wave sizing that trades wave count against per-wave size under cluster-capacity constraints (an optimization variant of the clamp semantics).

These directions are the intended research follow-up of the reference implementation; the component's strategy interface (the evaluation policy) is the extension point for each.
