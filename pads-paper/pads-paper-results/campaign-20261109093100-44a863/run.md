# PADS initial-results campaign run pads-campaign-20261109093100-44a863

- **Run ID:** pads-campaign-20261109093100-44a863
- **UTC date:** 2026-11-09T09:33:11Z
- **Branch commit:** 6f0ed16fb74155755679f99f75e48c18d77c469a (clean)
- **Cluster context:** k3s-test
- **Namespace:** cbse-pads-campaign-20261109093100-44a863
- **Project (SimulationExperiment):** pads-campaign-20261109093100-44a863
- **Campaign timeout:** 14400s (PADS_CAMPAIGN_TIMEOUT)
- **Design:** `<redacted-host>` (validated by the driver; 11 scenarios, statistical evaluation policy)

## Images (scrubbed per P5-R4: repository-name@digest, no registry host)

| Component | Reference |
|---|---|
| Paper-local Translator | translator@sha256:ef23f6b8c85cb703e59ed89f9703066020ea53e16b784b77ce3637e8748f66c0 |
| Experiment Operator | exop@sha256:8e18ba42d47711c8d69de52053a8857fcc690efd7dfbcb52ef8e7ed18f567d8f |
| Scenario Manager | sm@sha256:151fa5863eb25561dcbfd3078b01c0b60732077872cc4f7df6978bf38c2d008a |
| Reference EDS | eds@sha256:2f448f4900a31f23a4d1f2958e12b4998476eecacb328d235d83c33c4a93a0ae |
| PostProcessingService | pps@sha256:8f7bcffd80d7b7db9b50cb5db02ad878556942dfe4ce75d2aa279af29dc4944d |
| Scenario Detail Database | scenario-detail-database@sha256:c6c9a03f8971c3be65bfc3b5b65ad8ea47a1c25c5508005acbe8927f3f3e2700 |
| Runner base | runner-base@sha256:4d0d10319fd5cb55e851ea4f2178c23ed69b6c249585d281c52136641afb5c52 |
| BuildKit builder (locked) | moby/buildkit@sha256:60d1f642e29dc938bd6c109ba5500849fccf41921927c5339788b8227f57feb9 |
| Result DB Postgres (locked) | library/postgres@sha256:7341002d2b8c7c5bdd7542a671a95b36196c0b5b888daf454ae4fc33ba5346d7 |
| Generated runner A1 (reported by the chain) | cbse-test-runner@sha256:b3accf1a017100f24314197d3d1b210cd15637e544b43b12dfb29d9a0bf9ebc1 |
| Generated runner A2 (reported by the chain) | cbse-test-runner@sha256:58a4a3d6937364b976f71418c7ff7fa8cd153af42d3a666a6c4440a396371721 |
| Generated runner A3 (reported by the chain) | cbse-test-runner@sha256:4a7b167f2eacf62f58622a4c21797e0ad73eb0762be136b32439d7f5b4025536 |
| Generated runner A4 (reported by the chain) | cbse-test-runner@sha256:216748d520782b9e436e51f82623e70bac2f12927d05dfd5a0bfdd93a03ba00c |
| Generated runner A5 (reported by the chain) | cbse-test-runner@sha256:52ae36c1254b0da4ff86e516e05fab076d70ff784b147aaa100d4b89cd8e5c9a |
| Generated runner A6 (reported by the chain) | cbse-test-runner@sha256:8505ae9bc25644285a362de6d948d6a4f118ce3d69d4d59d2a0d7f65c3d62f55 |
| Generated runner A7 (reported by the chain) | cbse-test-runner@sha256:8246dfb9cf7c1c53062d1094a570fd94224c725eafe626d342f819fa50aa8563 |
| Generated runner A8 (reported by the chain) | cbse-test-runner@sha256:71444c404e4997015ec76c231ef8434ecc04ab51692d3c745bf70167e0cf25d3 |
| Generated runner B1 (reported by the chain) | cbse-test-runner@sha256:3094a71a37f0f8b7aed186e1a961a2ef7e8af6bd6fb4ace8c0cfcee356a6d412 |
| Generated runner B2 (reported by the chain) | cbse-test-runner@sha256:50d8ec36f7f4123b310f327f9f344e454a4a06270d97c8bb83e1db316563bd28 |
| Generated runner B3 (reported by the chain) | cbse-test-runner@sha256:5a73342d1ec824292f427385efd50e3795c5ec0461e7e1eb3714fea153bcdbb4 |

## Design summary

| Entry | Values |
|---|---|
| arrival_rate | 4, 6, 8, 10 |
| service_rate | 4, 5 |
| run_duration | 500 |
| seed_policy | 2027 |

| Scenario | Label | Recipe | λ | μ | ρ | Priority | Initial reps | ε |
|---|---|---|---|---|---|---|---|---|
0 | A1 | R1 | 4 | 4 | 0.5 | 1 | 20 | 0.05 |
1 | A2 | R2 | 6 | 4 | 0.75 | 1 | 20 | 0.05 |
2 | A3 | R3 | 8 | 4 | 1.0 | 1 | 20 | 0.05 |
3 | A4 | R4 | 10 | 4 | 1.25 | 1 | 20 | 0.05 |
4 | A5 | R5 | 4 | 5 | 0.4 | 1 | 20 | 0.05 |
5 | A6 | R6 | 6 | 5 | 0.6 | 1 | 20 | 0.05 |
6 | A7 | R7 | 8 | 5 | 0.8 | 1 | 20 | 0.05 |
7 | A8 | R8 | 10 | 5 | 1.0 | 1 | 20 | 0.05 |
8 | B1 | R3 | 8 | 4 | 1.0 | 2 | 10 | 0.2 |
9 | B2 | R3 | 8 | 4 | 1.0 | 2 | 10 | 0.1 |
10 | B3 | R3 | 8 | 4 | 1.0 | 2 | 10 | 0.05 |

## Per-scenario results

| Label | Scenario ID | Recipe | n | mean_wait_time | Half-width (95%) | Verdict | Runner rounds | created→published (s) | published→terminal (s) | total (s) |
|---|---|---|---|---|---|---|---|---|---|---|
A1 | 1 | R1 | 20 | 0.04960463278104997 | 0.0025050041078985216 | met | 1 | 1.22 | 37.44 | 38.66 |
A2 | 2 | R2 | 20 | 0.10752948689245609 | 0.0038118750952942336 | met | 1 | 6.23 | 35.13 | 41.36 |
A3 | 3 | R3 | 20 | 0.1843808143505127 | 0.007450522357638919 | met | 1 | 11.24 | 35.05 | 46.29 |
A4 | 4 | R4 | 20 | 0.2690313721993134 | 0.005936353516356885 | met | 1 | 16.25 | 35.06 | 51.31 |
A5 | 5 | R5 | 20 | 0.02701305236917459 | 0.0012545290940615822 | met | 1 | 21.26 | 35.06 | 56.32 |
A6 | 6 | R6 | 20 | 0.06071169926665317 | 0.0025061055803055326 | met | 1 | 26.27 | 35.05 | 61.32 |
A7 | 7 | R7 | 20 | 0.10661787301907828 | 0.0035313963282726335 | met | 1 | 31.28 | 35.07 | 66.35 |
A8 | 8 | R8 | 20 | 0.17215365179909747 | 0.003774763957876482 | met | 1 | 36.29 | 35.09 | 71.38 |
B1 | 9 | R3 | 10 | 0.18752948830140817 | 0.007234331273531796 | met | 1 | 41.56 | 34.84 | 76.40 |
B2 | 10 | R3 | 10 | 0.18495366722731538 | 0.008035197046423088 | met | 1 | 46.57 | 34.85 | 81.41 |
B3 | 11 | R3 | 10 | 0.18433130110110946 | 0.003273444692875411 | met | 1 | 51.57 | 34.86 | 86.44 |

## Assertion and replay outcomes

- A1: assert-results PASS (20 rows, recomputed verdict met); replay-seeds PASS (20 of 20 rows exact, SimPy 4.1.2)
- A2: assert-results PASS (20 rows, recomputed verdict met); replay-seeds PASS (20 of 20 rows exact, SimPy 4.1.2)
- A3: assert-results PASS (20 rows, recomputed verdict met); replay-seeds PASS (20 of 20 rows exact, SimPy 4.1.2)
- A4: assert-results PASS (20 rows, recomputed verdict met); replay-seeds PASS (20 of 20 rows exact, SimPy 4.1.2)
- A5: assert-results PASS (20 rows, recomputed verdict met); replay-seeds PASS (20 of 20 rows exact, SimPy 4.1.2)
- A6: assert-results PASS (20 rows, recomputed verdict met); replay-seeds PASS (20 of 20 rows exact, SimPy 4.1.2)
- A7: assert-results PASS (20 rows, recomputed verdict met); replay-seeds PASS (20 of 20 rows exact, SimPy 4.1.2)
- A8: assert-results PASS (20 rows, recomputed verdict met); replay-seeds PASS (20 of 20 rows exact, SimPy 4.1.2)
- B1: assert-results PASS (10 rows, recomputed verdict met); replay-seeds PASS (10 of 10 rows exact, SimPy 4.1.2)
- B2: assert-results PASS (10 rows, recomputed verdict met); replay-seeds PASS (10 of 10 rows exact, SimPy 4.1.2)
- B3: assert-results PASS (10 rows, recomputed verdict met); replay-seeds PASS (10 of 10 rows exact, SimPy 4.1.2)
## Experiment C replay (all Experiment-A rows)

- Replay: `replay-seeds.py` run offline over every Experiment-A row (8 scenarios, 160 rows)
- Outcome: 160 of 160 rows replayed exactly (SimPy 4.1.2, Python 3.14.8)
- Report: `experiment-c-replay-report.json`

## Offline precision illustration (Experiment B)

- Longest recorded B row set: B1 (10 rows)
- Deterministic order: recorded effective_seed ascending (deterministic; not the live order)
- Smallest seed-sorted prefix with 95% half-width <= epsilon:
  - epsilon=0.20: k=2
  - epsilon=0.10: k=2
  - epsilon=0.05: k=2
- Expected nondecreasing prefix relation (k(0.20) <= k(0.10) <= k(0.05)): holds
- Caveat: the seed-sorted order is a recorded-data ordering, not the live hostname-derived replication order; this illustrates the precision-replication trade on one common sequence, not the live adaptive allocation

## Analysis and plotting environment

- Analysis interpreter: Python 3.14.8
- Analysis tooling: `analyze.py` (pure stdlib) and `plot-figures.py` (matplotlib, pinned in `requirements-analysis.txt`)
- matplotlib: 3.11.2
- numpy: 2.5.4
- Figures: `figures/f1-*.svg` ... `figures/f4-*.svg` (deterministic SVG output, fixed svg.hashsalt)

## Unchanged smoke receipt (same settlement)

smoke run 20261105152950-6d0ee4: 15/15 specs passed (SKIP_BUILD=1 over published 26.11.4 digest set, 2026-11-05)
