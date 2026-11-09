# Campaign evidence (placeholder)

This directory holds the committed, **sanitized** campaign evidence for
the P6 initial-results campaign. It is placeholder-only in this slice.

- Evidence is produced **only** at settlement by a live
  `pads-paper/acceptance/run-experiment.sh --design
  pads-paper/results/campaign/design.json` run; no file in this
  directory may be hand-written to look like run output.
- Each run lives in `evidence/<run-id>/` where the run id is
  `pads-campaign-<UTCstamp>-<6hex>`.
- The per-run structure (P6-R2): `run.md` (campaign provenance, with
  `{{P6-ANALYSIS:...}}` markers completed by `analyze.py`), the
  verbatim validated `design.json`, `eds-state.json`, the full
  `scenario-status.json`, and a `scenarios/<label>/` directory per
  scenario holding `result-rows.json`, `scenario-status.json`,
  `assertions.txt`, and `replay-report.json`.
- Sanitization follows the P5-R4 rules (see
  `pads-paper/acceptance/README.md`): no credentials, registry
  hostnames, cluster endpoints/URLs, node/pod hostnames, or kubeconfig
  paths; image references appear as `<repository-name>@<digest>` only.
- Raw, unsanitized exports remain untracked under `artifacts/pads/`.
