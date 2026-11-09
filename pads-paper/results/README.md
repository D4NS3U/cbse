# PADS paper — initial results bundle (P6)

Tooling and (at settlement) committed evidence for the P6 slice
(`devlog/changes/pads-paper/slices/P6-paper-result-bundle.md`). The
slice executes the ruled initial-results campaign — Experiments A, B, C
from `devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md` — through the
live CBSE chain with the paper-local `erlang_a_v1` Translator, then
produces this committed, sanitized bundle.

## Bundle layout

| Path | Role |
|---|---|
| `campaign/design.json` | The normative campaign design (`pads-campaign-design-v1`): eight entries, eight recipes (R1–R8), eleven scenarios (A1–A8, B1–B3). Loaded and validated by every tool in this bundle; the driver validates it in preflight before any cluster mutation. |
| `analysis/campaign.py` | Pure-stdlib design loading, validation, and planning. Derives the full scenario plan (baked parameters, experiment group, offered load `rho`) and the wall-clock stage durations from Core DB timestamps. |
| `analysis/analyze.py` | Pure-stdlib offline analysis over a sanitized campaign evidence directory plus the design: per-scenario statistics (means, Student-t 95% half-widths, recomputed verdicts, abandonment intervals, throughput/utilization means, runner-round fields, stage durations), campaign aggregates per experiment group, the Experiment-C replay, and the Experiment-B precision illustration. Emits `analysis.json`, `analysis.csv`, `experiment-c-replay-report.json`, and completes the `run.md` markers. |
| `analysis/plot-figures.py` | Deterministic SVG rendering (pinned matplotlib) of exactly four figures: F1 mean wait vs rho with 95% CI error bars (one series per mu), F2 abandonment vs rho, F3 mean throughput and utilization vs rho, F4 final replications and runner rounds per A scenario. |
| `analysis/requirements-analysis.txt` | Pinned plotting environment (`matplotlib==3.11.2`, `numpy==2.5.4`). Analysis itself is pure stdlib. |
| `analysis/test_analysis.py` | Offline unit tests (`python3 -m unittest`): plan derivation, rho, stage durations, statistics, t-quantile usage, verdict recomputation, label mapping, CSV/JSON emission, and the matplotlib-gated byte-stability test. |
| `evidence/` | Committed, sanitized campaign evidence, populated **only** at settlement (see `evidence/README.md`). Placeholder-only in this slice. |
| `LIMITATIONS.md` | The limitations document (P6-R4). |

## Reproduction path

Two distinct reproduction guarantees, matching the slice's ruling:

1. **Process reproduction.** `campaign/design.json` (the ruled design) +
   this tooling + the published immutable image digests reproduce the
   *process*: the campaign driver
   (`pads-paper/acceptance/run-experiment.sh --design`) is re-runnable
   against a test cluster with the settlement environment; the design
   validation, the EDS entry/recipe/scenario flow, the adaptive PPS
   loop, the exports, and the assertions are all deterministic given
   the same environment.
2. **Data reproduction.** The recorded evidence in `evidence/<run-id>/`
   reproduces the *data*: `analyze.py` re-computes every reported
   statistic from the committed result rows, `replay-seeds.py` re-runs
   the canonical model on the recorded effective seeds with exact-value
   comparison (Experiment C), and `plot-figures.py` re-renders the
   figures byte-identically from `analysis.json`.

The analysis and plotting steps run **offline** over committed evidence
only — no cluster, registry, or network access.

## Environment

- Analysis (`analyze.py`, `campaign.py`, tests): system `python3`, pure
  stdlib. SimPy 4.1.2 is required for the Experiment-C replay
  (`replay-seeds.py`) and its tests skip cleanly without it, mirroring
  the repository conformance convention.
- Plotting (`plot-figures.py`): Python environment with
  `requirements-analysis.txt` installed (pinned versions; see
  `LIMITATIONS.md` for the analysis-environment note).

## Normative documents

- `devlog/changes/pads-paper/slices/P6-paper-result-bundle.md` — slice
  requirements (P6-R1…P6-R6) and verification tiers.
- `devlog/changes/pads-paper/INITIAL_RESULTS_PLAN.md` — research
  questions, fixed model configuration, and the Experiment A/B/C
  rulings.
- `devlog/changes/pads-paper/FEATURE.md` — feature contract.
- `pads-paper/acceptance/README.md` — the driver, the `--design` mode,
  the environment contract, and the sanitization rules (P5-R4) that the
  campaign evidence follows.
- `devlog/changes/pads-paper/IMPLEMENTATION_HANDOFF.md` — implementation
  handoff.

## Evidence policy

Evidence is committed only at settlement, produced exclusively by a live
`--design` run of the driver, sanitized by the same deterministic rules
as P5-R4 (documented in `pads-paper/acceptance/README.md`), and never
hand-fabricated. Raw, untracked exports stay under `artifacts/pads/`
(gitignored). No credentials, registry hostnames, endpoints, or
kubeconfig paths appear in any tracked file in this directory.
