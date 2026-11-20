# PADS paper results bundle

Local copy of the CBSE PADS-paper evidence, extracted from the committed
`PADS-paper` branch at commit `fddbff6` (2026-11-09). The repository remains
the source of truth; this bundle is a convenience copy for paper writing.

## Contents

### `campaign-20261109093100-44a863/` — the initial-results campaign (Experiments A, B, C)

The complete sanitized evidence of the live 11-scenario campaign run
(`pads-campaign-20261109093100-44a863`, cluster context k3s-test):

- `run.md` — provenance manifest: run id, UTC date, branch commit, all image
  digests (scrubbed to repository@digest), design summary, per-scenario table
  (settings, n, mean, half-width, verdict, rounds, stage durations), the
  Experiment-C replay outcome, the Experiment-B prefix illustration, the
  analysis environment, and the unchanged-smoke receipt.
- `analysis.json` / `analysis.csv` — per-scenario statistics (Student-t 95%
  half-widths, recomputed verdicts, abandonment intervals, throughput and
  utilization means, runner rounds, wall-clock stage durations) and group
  aggregates. `analysis.json` is the richest machine-readable source for
  tables; `analysis.csv` is the flat view.
- `figures/` — the four deterministic SVG figures:
  - `f1-mean-wait-vs-rho.svg` — mean waiting time vs offered load with 95% CI,
    one series per service rate.
  - `f2-abandonment-vs-rho.svg` — abandonment probability vs offered load.
  - `f3-throughput-utilization-vs-rho.svg` — mean throughput and server
    utilization vs offered load.
  - `f4-reps-rounds-per-scenario.svg` — replications and runner rounds per
    Experiment-A scenario.
  Regeneration is byte-stable: `plot-figures.py` with the pinned
  `analysis-requirements.txt` environment.
- `experiment-c-replay-report.json` — recorded-effective-seed replay over all
  160 Experiment-A rows (all exact; SimPy 4.1.2).
- `scenarios/<label>/` — per-scenario raw data: `result-rows.json` (every
  replication row: the 15-field erlang_a_v1 record incl. effective seed),
  `scenario-status.json` (the Core DB record with timing/round fields),
  `assertions.txt`, `replay-report.json`.
- `design.json` — the campaign design as executed (entries, recipes R1–R8,
  scenarios A1–A8 and B1–B3).
- `eds-state.json`, `scenario-status.json` — the exported orchestration state.

### `acceptance-p5/pads-20261105154051-121fcd/` — the P5 end-to-end acceptance run

Single-scenario live proof (λ=4, μ=4, T=500, seed 2027, 5 reps, ε=0.05):
result rows, replay report, verdict record (met, half-width 0.0078), EDS state,
and the run manifest.

### `citations/`

- `MODEL_SELECTION.md` — the model-selection rationale with the verified
  academic anchors (Garnett/Mandelbaum/Reiman 2002; Mandelbaum/Zeltyn; Whitt
  2006) and the SimPy Bank Renege provenance (upstream example vs CBSE's
  original two-server exponential-patience generalization).
- `INITIAL_RESULTS_PLAN.md` — the research questions (RQ1–RQ4), experiment
  definitions, statistical presentation rules, and reproducibility bundle
  requirements that this campaign implements.

### Top level

- `LIMITATIONS.md` — the ruled limitations list (hostname-derived production
  seeds and the replay caveat, single campaign run, adaptive-stopping
  inference caution, ensemble-vs-individual monotonicity, synthetic
  parameters, fixed model constants).
- `campaign-design.json` — the normative design (same content as
  `campaign-.../design.json`).
- `analysis-requirements.txt` — the pinned analysis environment
  (matplotlib/numpy) for reproducing the figures.

## Key numbers at a glance (Experiment A, μ=4 and μ=5)

| Label | λ | μ | ρ | n | mean_wait_time ± hw (95%) | abandonment ± hw | verdict |
|---|---|---|---|---|---|---|---|
| A1 | 4 | 4 | 0.50 | 20 | 0.04960 ± 0.00251 | 0.0490 ± 0.0038 | met |
| A2 | 6 | 4 | 0.75 | 20 | 0.10753 ± 0.00381 | 0.1081 ± 0.0041 | met |
| A3 | 8 | 4 | 1.00 | 20 | 0.18438 ± 0.00745 | 0.1848 ± 0.0061 | met |
| A4 | 10 | 4 | 1.25 | 20 | 0.26903 ± 0.00594 | 0.2674 ± 0.0047 | met |
| A5 | 4 | 5 | 0.40 | 20 | 0.02701 ± 0.00125 | 0.0263 ± 0.0020 | met |
| A6 | 6 | 5 | 0.60 | 20 | 0.06071 ± 0.00251 | 0.0602 ± 0.0026 | met |
| A7 | 8 | 5 | 0.80 | 20 | 0.10662 ± 0.00353 | 0.1057 ± 0.0038 | met |
| A8 | 10 | 5 | 1.00 | 20 | 0.17215 ± 0.00377 | 0.1727 ± 0.0039 | met |

Experiment B (ρ=1 boundary, ε ∈ {0.20, 0.10, 0.05}): B1 0.18753±0.00723,
B2 0.18495±0.00804, B3 0.18433±0.00327 — all met at the initial 10
replications. Experiment C: 160/160 exact replays.

## Provenance

- Branch: `PADS-paper`, commit `fddbff6` (remote: `origin/PADS-paper`).
- Campaign run: `pads-campaign-20261109093100-44a863` (2026-11-09).
- Acceptance run: `pads-20261105154051-121fcd` (2026-11-05).
- Unchanged 15-spec smoke receipt referenced by the campaign:
  run `20261105152950-6d0ee4` (15/15, 2026-11-05).
- Every result row traces to a parameterset id, scenario id, effective seed,
  and immutable image digest recorded in the manifests.
