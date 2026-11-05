# PADS acceptance evidence

This directory holds sanitized evidence from live PADS end-to-end
acceptance runs, created by `run-experiment.sh` at settlement time as
`evidence/<run-id>/` subdirectories.

- It is **placeholder-only in this slice**: no live-run evidence is
  committed here, and no fabricated run data may be added by hand.
- Committed evidence must pass the sanitization rules documented in
  `../README.md` (section "Evidence and sanitization policy").
- Raw (un-sanitized) artifacts live under `artifacts/pads/<run-id>/` at
  the repository root and are never committed.
