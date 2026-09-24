# Dispatch prompt — A4: cbse-labs preparation (Task-spec-contract output)

**Task title:** A4 — cbse-labs preparation (manifest-only: content inventory, move-list, env-provider template, cluster-smoke preservation + relocation runbook)

**Read order (mandatory, all normative and read-only for you):** 1) `devlog/changes/public-repo-hygiene/FEATURE.md` (umbrella; esp. §5, §6 partition, §7 recipes); 2) `devlog/changes/public-repo-hygiene/slices/A4-cbse-labs-preparation.md` (your slice — fully self-contained; it owns every concrete requirement, the R1 rule, and the exact deliverable file list); 3) repository-root `AGENTS.md` (whatever text is on disk at your start — post-A3-rewrite if A3 landed first; do not edit it). On any contradiction: stop and ask via your ask channel.

**Target (five-part contract, summarized — the slice file is normative):** you own exactly **new files** you create under `devlog/changes/repo-professionalization/labs/` — nothing else. The deliverables (exact names): `labs/inventory.md`, `labs/move-list.md`, `labs/env-provider.template.sh`, `labs/cluster-smoke-relocation.md`, and the byte-identical preserved copy `labs/ci/cluster-smoke.yml` (`cp` + `cmp` proof). You execute no moves, no deletions, no external actions — R1: relocation is user-executed at D2; P6: user owns all git and external operations; your manifests⊇ are documentation, not execution.

**Change:** author the cbse-labs content inventory (MANAGER.md, `agents/**`, `devlog/**` wholesale incl. this program's directories, `docs/project-status.md`, `artifacts/` note), the relocation runbook with the cbse-labs skeleton from the charter §5 and the stays-public list, the placeholder-only env-provider template for `CBSE_REGISTRY` / `CBSE_REGISTRY_AUTH_FILE` / `KUBECONFIG`, and the cluster-smoke relocation runbook with the preserved copy's checksums. The env-provider and inventory/move-list must be free of private values (P5); quoting the private workflow is legal **only** where unavoidable in the relocation runbook and the preserved copy itself.

**Constraints (binding summary; umbrella §5 carries P1–P8 verbatim):** must-not-edit: `PROBLEM.md`, anything under `../prompts/`, any spec in `devlog/changes/public-repo-hygiene/`, `MANAGER.md`, `AGENTS.md`, every other path on disk. The preserved copy stays byte-identical — no "improvements". Secrets hygiene: never echo credential-file contents. No cluster operations, no network. Scope discipline: new-file creation only.

**Ownership:** sole owner of the new `labs/**` tree; your parallel wave-mate owns `Makefile`/`AGENTS.md`/`docs/CBSE_TESTING_GUIDE.md` — co-resident modifications there are expected, never yours to touch or revert.

**Observable acceptance:** the eight evidence blocks in the slice file, all echoed in your report — including the `cmp`/`shasum` preserved-copy proof, the template hygiene grep, the R1 integrity check, and `make test-fast` rc=0 as the charter no-collateral proof. Also state whether the on-disk `AGENTS.md` at your start was pre- or post-A3-rewrite — the manager records the R2 observation.

**Mandatory protocol for this worker (no exceptions):**
- **Runtime attestation, first checkpoint:** run `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"`; repeat verbatim in your `worker_done` executive summary; if it is not `ai.forge/qwen3.8-27b-nvfp4`: stop immediately, report `--outcome failed` with the observed line.
- **No commits (P6):** your new files stay untracked in the working tree; the user commits.
- **Read-only specs:** the charter, program prompts, umbrella FEATURE, slices, handoff, `MANAGER.md` — never edited by you.
- **Completion:** report `worker_done` with: three-sentence executive summary; both lifecycle IDs (task id and dispatch id); explicit `--outcome succeeded|failed`; the verbatim attestation line; all eight evidence-block outputs per the slice file.
