# Dispatch prompt — A2: Dead-trees deletion (Task-spec-contract output)

**Task title:** A2 — Dead-trees deletion (alpha2/alpha3 API trees, user-confirmed; the one doc reference fix; two deletions explicitly blocked pending)

**Read order (mandatory, all normative and read-only for you):** 1) `devlog/changes/public-repo-hygiene/FEATURE.md` (umbrella; esp. §5, §6 partition, §7 recipes); 2) `devlog/changes/public-repo-hygiene/slices/A2-dead-trees.md` (your slice — fully self-contained; it owns every concrete requirement); 3) repository-root `AGENTS.md` (current text governs your run). Then the trees you delete. On any contradiction: stop and ask via your ask channel.

**Target (five-part contract, summarized — the slice file is normative):** delete exactly `experiment-operator/api/alpha2/**` and `experiment-operator/api/alpha3/**` (user-confirmed via D1), and fix exactly the one `docs/COMPONENT_DESIGN_GOALS.md` list entry (line ~406) that links `api/alpha3/simulationexperiment_types.go` — it re-points at `../devlog/changes/README.md` with the parenthetical the slice prescribes. **Blocked, absolutely not yours:** `test/compat/eds-sm/` and the root `hack/` stay untouched (user confirmation pending; `experiment-operator/hack/` is a different, live directory and also stays).

**Change:** remove the retired, unreconciled trees; repair the dangling reference; prove the live tree is byte-clean without them.

**Constraints (binding summary; umbrella §5 carries P1–P8 verbatim):** this is a Go-tree change — `make test-fast` (including `verify-generated`) must be rc=0 **after your deletions**; test integrity: never weaken/delete/skip anything to make it pass; if a test fails you report `--outcome failed` with the exact failing output and your analysis. Scoped acceptance: you are judged only on your deletions and your one doc line — private strings elsewhere (A3's files) are not yours. No cluster operations, no `make test-smoke`, no registry/network actions. Scope discipline: no "nearby" cleanup; discovered gaps → report lines.

**Ownership:** sole owner of the two API trees (as deletions) and the single doc line; your parallel wave-mate owns A1's partition — never touch their files, nor they yours.

**Observable acceptance:** the five evidence blocks in the slice file, all echoed in your report — the manager re-runs each independently, including re-running `make test-fast` itself from a fresh shell.

**Mandatory protocol for this worker (no exceptions):**
- **Runtime attestation, first checkpoint:** run `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"`; repeat verbatim in your `worker_done` executive summary; if it is not `ai.forge/qwen3.8-27b-nvfp4`: stop immediately, report `--outcome failed` with the observed line.
- **No commits (P6):** deletions and the doc fix stay in the working tree; the user commits.
- **Read-only specs:** `FEATURE.md`, slice files, `AGENTS.md`, `MANAGER.md`, `devlog/**` (including the devlog target-line you will fix — you change the `docs/` side, never devlog content) — you never edit them.
- **Completion:** report `worker_done` with: three-sentence executive summary; both lifecycle IDs (task id and dispatch id); explicit `--outcome succeeded|failed`; the verbatim attestation line; all evidence-block outputs per the slice file.
