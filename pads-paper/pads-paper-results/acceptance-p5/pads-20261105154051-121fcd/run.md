# PADS end-to-end acceptance run pads-20261105154051-121fcd

- **Run ID:** pads-20261105154051-121fcd
- **UTC date:** 2026-11-05T15:42:33Z
- **Branch commit:** bd6a1fb16bd4edd9fbe58a8a963b2d0096de6277 (clean)
- **Cluster context:** k3s-test
- **Namespace:** cbse-pads-20261105154051-121fcd
- **Project (SimulationExperiment):** pads-20261105154051-121fcd

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
| Generated runner (reported by the chain) | cbse-test-runner@sha256:c7e38b50e233632dd7ea6c23e27950d4435b886b2a08dff63df9f6a3929c76e6 |

## Scenario settings

| Field | Value |
|---|---|
| arrival_rate | 4 |
| service_rate | 4 |
| run_duration | 500 |
| seed_policy | 2027 |
| priority | 5 |
| number_of_reps | 5 |
| confidence_metric | 0.05 |
| PPS evaluation policy | statistical (default) |

## Verdict summary

- Final sample mean (mean_wait_time): 0.04931037315538547
- Half-width (95% CI, Student-t): 0.007807407245939874
- Verdict: met (scenario state Finished; recomputed verdict: met)
- Computed replications: 5
- Additional-runner rounds: 0 (runner_round 1)

## Assertion and replay outcomes

- assert-results: PASS — 5 rows, exact 15-field contract, distinct effective seeds, count matches computed replications
- replay-seeds: PASS — 5 of 5 rows replayed exactly (SimPy 4.1.2)

## Unchanged smoke receipt (same settlement)

smoke run 20261105152950-6d0ee4: 15/15 specs passed (SKIP_BUILD=1 over published 26.11.4 digest set, 2026-11-05)
