# P6 — Paper-result bundle

## Target

Run the ruled initial-results campaign (Experiments A, B, C from `INITIAL_RESULTS_PLAN.md`) through the live CBSE chain with the paper-local `erlang_a_v1` Translator, and produce the committed, sanitized paper-result bundle: per-scenario result rows and orchestration records, aggregate statistics, figures, a provenance manifest, the replay check, and the limitations document.

The campaign is one live run: one stack, one experiment, one EDS design with eleven scenarios (A1–A8, B1–B3). Experiment C is the offline recorded-effective-seed replay over the Experiment-A rows. Analysis and plotting run offline over the committed sanitized evidence.

## Dependency

- P5 is complete and landed at `58b34fd`; the published paper-local Translator image digest (provenance tag `26.11.5.sha-bd6a1fb16bd4-…`) remains valid because P6 does not change the Translator component.

## Requirements

### P6-R1 — Campaign design (normative)

Create `pads-paper/results/campaign/design.json`, the single source of truth for the campaign, validated by the driver:

- **Entries:** `arrival_rate` ∈ {4, 6, 8, 10}; `service_rate` ∈ {4, 5}; `run_duration` = 500; `seed_policy` = 2027.
- **Recipes R1–R8** with labels: (λ, μ) = (4,4), (6,4), (8,4), (10,4), (4,5), (6,5), (8,5), (10,5) — offered loads ρ = λ/(2μ) of 0.50, 0.75, 1.00, 1.25, 0.40, 0.60, 0.80, 1.00.
- **Scenarios:**
  - A1–A8, one per recipe R1–R8: priority 1, `number_of_reps` 20, `confidence_metric` 0.05.
  - B1–B3, all on recipe R3 (λ=8, μ=4): priority 2, `number_of_reps` 10, `confidence_metric` 0.20 / 0.10 / 0.05.
- Statistical evaluation policy (default). Creation order is A1–A8 then B1–B3; the design carries labels that the analysis maps onto Core DB scenario ids deterministically (scenario order plus the recipe/parameterset mapping from the exported EDS state).

### P6-R2 — Design-driven driver extension

Extend `pads-paper/acceptance/run-experiment.sh` with an optional `--design <design.json>` mode while keeping the default (no flag) byte-equivalent in behavior to the approved P5 single-scenario acceptance path:

- With `--design`: validate the design (unique entry values per parameter, resolvable recipe references, valid scenario settings, labeled scenarios), create all entries/recipes/scenarios through the same EDS API, save, and start once.
- Wait for **all** scenarios to reach the terminal `Finished` state (a `Failed` scenario fails the campaign), using a campaign timeout from `PADS_CAMPAIGN_TIMEOUT` (default 4h; the single-scenario default remains unchanged).
- Export per scenario under the raw artifact dir: result rows, the Core DB scenario record, and the experiment status; plus the full EDS state once.
- Run `assert-results.py` per scenario with a per-scenario context file built from the design and the Core DB record, and `replay-seeds.py` per scenario; any failure fails the campaign.
- Write sanitized evidence to `pads-paper/results/evidence/pads-campaign-<run-id>/` with per-scenario structure, the same sanitization rules as P5-R4, and a campaign `run.md` skeleton (provenance completed by the analysis step).
- Teardown, preflight, refusal, and cluster-safety behavior remain exactly the P5 contract.

`pads-paper/acceptance/README.md` documents the `--design` mode; `test_acceptance.py` gains offline tests for design validation and a multi-scenario stub dry-run; the existing single-scenario refusal and dry-run tests remain green.

### P6-R3 — Analysis tooling

Create `pads-paper/results/analysis/` with:

- `analyze.py` (pure stdlib): consumes the sanitized campaign evidence directory and the design; emits per-scenario records and aggregates:
  - per scenario: label, λ, μ, ρ, final `n` (= computed replications), `mean_wait_time` sample mean, Student-t 95% half-width, recomputed verdict (`met` iff half-width ≤ ε; `stop_unmet` recorded as-is), `abandonment_probability` mean with Student-t 95% interval (the documented interval choice), mean throughput, mean server utilization, runner rounds (`runner_round`, `round_reps`, `round_computed_reps`), and wall-clock stage durations derived from the Core DB timestamps (created → translation request published → terminal updated);
  - campaign aggregate: totals per experiment group (A, B);
  - CSV and JSON outputs into the evidence directory.
- `plot-figures.py` (matplotlib, pinned in `requirements-analysis.txt`): renders exactly four SVG figures from the aggregates:
  - F1: `mean_wait_time` vs ρ with 95% CI error bars, one series per μ;
  - F2: `abandonment_probability` vs ρ with 95% CI error bars;
  - F3: mean throughput and mean server utilization vs ρ;
  - F4: final replications and runner rounds per A scenario.
  - SVG output must be deterministic (stable metadata/`svg.hashsalt`; no timestamps), so regenerating from identical data is byte-stable.
- `test_analysis.py` (pure stdlib): unit tests for `analyze.py` (statistics, t-quantile usage, verdict recomputation, label mapping, ρ computation, stage durations from synthetic timestamps, CSV/JSON emission) and a byte-stability test mode for `plot-figures.py`.

The analysis runs offline over committed evidence only; it performs no cluster, registry, or network access.

### P6-R4 — Provenance manifest and limitations

- The campaign `run.md` in the evidence directory records: run id, UTC date, branch commit and dirty state, cluster context name, all image digests (scrubbed per P5-R4 — the paper-local Translator digest from the P5 publication reused unchanged, the 26.11.4 baseline set, locked builder/Postgres), the design summary, the per-scenario table (settings, n, mean, half-width, verdict, rounds, stage durations), the Experiment-C replay outcome, the offline common-sequence precision illustration, the analysis/plotting environment versions, and the referenced unchanged-smoke receipt from the same settlement day (reused per P6-R6).
- The offline precision illustration for Experiment B: from the longest recorded B row set, in a deterministic seed-sorted order (documented), compute the smallest prefix whose sample half-width satisfies each ε ∈ {0.20, 0.10, 0.05}; the expected nondecreasing prefix relation is reported, with the order caveat documented.
- `pads-paper/results/LIMITATIONS.md` documents: hostname-derived production seeds (exact whole-experiment replay not promised; model-level replay uses recorded effective seeds), single campaign run on one test cluster, adaptive stopping complicating naive between-scenario inference, ensemble-trend rather than per-replication monotonicity, stop_unmet interpretation, synthetic parameters without real call-center calibration, the fixed warm-up/patience constants, the analysis environment, and the P5-noted accepted omissions.
- `pads-paper/results/README.md` explains the bundle layout, the reproduction path (design + tooling + published digests reproduce the process; the recorded evidence reproduces the data), and links the normative plan documents.

### P6-R5 — Experiment C replay

At settlement, `replay-seeds.py` runs over every Experiment-A row (the offline check; the driver already replays per scenario live). The committed replay report covers all A rows with per-row exact-value comparison and the SimPy/Python versions used.

### P6-R6 — Scope, hygiene, and reuse

- Worker ownership: `pads-paper/results/**` (new), plus the ruled minimal extension of `pads-paper/acceptance/run-experiment.sh`, `README.md`, and `test_acceptance.py`. Nothing else may change.
- Do not modify the Translator/EDS/PPS components, shared harness, e2e manifests, devlog specs, or `pads-paper/simulation-model/**`.
- No cluster access in the worker lane; the live campaign is settlement-owned (coordinator-executed with the user-confirmed environment, reusing the P5-published Translator digest — no new image publication because no image content changed).
- The unchanged 15-spec smoke need not be re-run at P6 settlement: no shared code, manifest, or harness-built image changed since the same-day receipt `20261105152950-6d0ee4` (15/15) over the identical digest set; the campaign driver references that receipt in `run.md`.
- No credentials, registry hostnames, endpoints, kubeconfig paths, or generated artifacts in tracked files; raw campaign output stays untracked under `artifacts/pads/`.

## Verification tiers

- **Worker (offline):** `bash -n` both shell scripts; `python3 -m unittest` in `pads-paper/acceptance` and `pads-paper/results/analysis` (matplotlib-dependent tests proven through a temporary untracked venv, including SVG byte-stability); refusal paths unchanged; default-mode stub dry-run still green; multi-scenario design-mode stub dry-run green; repository-root `make test-fast`; `git diff --check`.
- **Settlement (coordinator, cluster):** execute the campaign with `--design`; require all eleven scenarios `Finished`, per-scenario assertions and replay green; run the analysis and plotting over the sanitized evidence; commit the bundle; update the ledger. Any failed receipt is `verification-blocked`.

## Observable acceptance (worker lane)

1. `design.json` matches P6-R1 exactly; the driver validates it and refuses malformed designs offline.
2. The default (no `--design`) driver path is unchanged: existing refusal tests and the single-scenario stub dry-run still pass.
3. A multi-scenario stub dry-run produces the ruled per-scenario evidence structure with correct sanitization.
4. `python3 -m unittest` passes in both tool directories; matplotlib tests are proven via a temporary venv with byte-stable SVG regeneration.
5. `git diff --check` passes; the diff touches only the owned paths.
6. Repository-root `make test-fast` passes.
7. The implementation commit carries the assigned orchestration task/dispatch trailer.

## Completion

P6 is `complete` only after independent Pi/GLM review returns `approve` against the implementation HEAD **and** the settlement campaign passes: eleven `Finished` scenarios, per-scenario assertion and replay receipts, the analysis/figure bundle, the provenance manifest, and the committed sanitized evidence. Only then are the approved child branch and the results bundle landed on `PADS-paper`.
