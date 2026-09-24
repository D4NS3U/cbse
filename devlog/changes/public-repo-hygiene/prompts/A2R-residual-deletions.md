# Dispatch prompt — A2R: Residual dead-trees deletion (Task-spec-contract output)

**Task title:** A2R — Residual dead-trees deletion (`test/compat/eds-sm/` + root `hack/`), user-confirmed via D1

**Read order (mandatory, all normative and read-only for you):** 1) `devlog/changes/public-repo-hygiene/FEATURE.md` (umbrella; esp. §5 global constraints, §6 ownership partition, §7 shared recipes); 2) `devlog/changes/public-repo-hygiene/slices/A2-dead-trees.md` (the parent slice — its blocked sub-step is now unblocked for exactly these items); 3) repository-root `AGENTS.md` — note this is the **rewritten public-tone text**; your run is its first post-rewrite observation (R2), so your evidence must show you worked squarely within it. On any contradiction: stop and ask via your ask channel.

**Target (five-part contract):** delete exactly `test/compat/eds-sm/**` (user-confirmed D1 residual — user confirmation received 2026-09-24) and the root `hack/` empty directory; after deleting eds-sm, also remove the then-empty `test/compat/` directory (filesystem-only, same rule as `hack/`). Pre-verified evidence you may rely on: eds-sm holds only a standalone manual alpha-era flow (shell script + YAML manifests + two READMEs); zero references exist in tracked build/test code (the sole other mention is inside the untracked `.kilo/` worktree tombstone — ignore and never touch `.kilo/`); root `hack/` is empty and untracked. **Untouchable:** everything else — in particular `experiment-operator/hack/` (a different, live directory used by the operator Makefile's deepcopy target) and all of `test/harness/**` (R4).

**Change:** remove the confirmed retired items; prove the live tree is clean without them.

**Constraints:** no cluster operations, no kubectl, no registry/network actions, no commits (P6 — leave everything in the working tree for user review); scope discipline — nothing beyond the named paths; discovered gaps → report lines, never ownership handwaves; secrets hygiene as umbrella §5. The current (rewritten) `AGENTS.md` does not require the cluster tier for this change — run nothing cluster-side.

**Observable acceptance (all echoed in your worker_done; the manager re-runs each):**
1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat in your executive summary; mismatch → stop, change nothing, `--outcome failed`).
2. Containment: `git status --short` shows only ` D` deletions under `test/compat/eds-sm/` (plus expected co-resident entries of the in-flight A3R wave-mate under `.github/`, and the settled-but-uncommitted Package-A change set already in the tree — zero entries attributable to you outside your paths).
3. Deletion proof: `ls test/compat` and `ls hack` report "No such file or directory" (both dirs gone, incl. the emptied parent).
4. Reference proof: `git grep -n "compat/eds-sm"` → empty (rc=1) after deletion; `git grep -n "eds-sm"` → only devlog historical mentions under `devlog/changes/**` are acceptable.
5. Run the R3 link-check recipe (umbrella §7) over the full tree → no new BROKEN lines.
6. Run `make test-fast` at completion and record rc=0 (charter §8 evidence; this run is also the first R2 observation under the rewritten `AGENTS.md`). Echo the trailing rc line. If anything fails: `--outcome failed` with the exact failing output — never work around, never weaken a test.
