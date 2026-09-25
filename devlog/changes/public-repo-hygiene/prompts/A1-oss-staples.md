# Dispatch prompt — A1: OSS staples (Task-spec-contract output)

**Task title:** A1 — OSS staples (README product rewrite, CONTRIBUTING, CHANGELOG, SECURITY, LICENSE verification)

**Read order (mandatory, all normative and read-only for you):** 1) `devlog/changes/public-repo-hygiene/FEATURE.md` (umbrella; esp. §5 global constraints, §6 ownership partition, §7 shared recipes); 2) `devlog/changes/public-repo-hygiene/slices/A1-oss-staples.md` (your slice — fully self-contained; it owns every concrete requirement); 3) repository-root `AGENTS.md` (current text governs your run). Then the files themselves. On any contradiction the slice file cannot resolve against the tree: stop and ask via your ask channel — never resolve by opinion, never edit any spec.

**Target (five-part contract, summarized — the slice file is normative):** you own exactly `README.md` (product rewrite), `CONTRIBUTING.md` (new), `CHANGELOG.md` (new, Keep-a-Changelog seeded from `git log` at feature granularity), `SECURITY.md` (new, placeholder disclosure contact reported as open user decision), and `LICENSE` (**verification only — deviations reported, never edited**).

**Change:** make the repository surface a product: quickstart (source path today, chart forward-reference), architecture, truthful testing section with environment-provided placeholders only, contributor/runbook and security staples per the slice's numbered Change items.

**Constraints (binding summary; umbrella §5 carries P1–P8 verbatim):** documentation-only slice — no Go/CRD/Dockerfile/harness changes, no cluster operations, no kubectl, no registry/network actions; no private values anywhere in your files (`registry.unibw.de`, `i31bdase`, `192.168.*`, `/home/*/.kube*`, `cbse-k3s`, `ai.forge` are all forbidden in your output); no fabricated contacts or URLs — placeholder + report; scope discipline: nothing outside your five paths; discovered gaps go in your report, not your diffs; secrets hygiene: never echo `CBSE_REGISTRY_AUTH_FILE` contents or any credential material.

**Ownership:** sole owner of your five paths; your parallel wave-mate owns A2's partition (api trees + one `docs/COMPONENT_DESIGN_GOALS.md` line) — never touch their files.

**Observable acceptance:** the seven evidence blocks in the slice file, all echoed in your report — the manager re-runs each independently from a fresh shell before accepting settlement.

**Mandatory protocol for this worker (no exceptions):**
- **Runtime attestation, first checkpoint:** run `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"`; note the exact output line; repeat it verbatim in your `worker_done` executive summary. If it is not `ai.forge/qwen3.8-27b-nvfp4`: stop immediately, change nothing, and report `--outcome failed` with the observed line.
- **No commits (P6):** all changes stay in the working tree; the user commits.
- **No test-tier shortcuts:** run `make test-fast` at your final checkpoint and echo its `rc=0` receipt — mandatory evidence even though you changed no Go files (charter no-collateral proof).
- **Read-only specs:** `FEATURE.md`, slice files, `AGENTS.md`, `MANAGER.md`, everything under `devlog/` — you never edit them.
- **Completion:** report `worker_done` with: three-sentence executive summary; both lifecycle IDs (your task id and dispatch id); explicit `--outcome succeeded|failed`; the verbatim attestation line; the LICENSE/header findings; all evidence-block outputs per the slice file.
