# Dispatch prompt — A3R: Public cluster-smoke.yml deletion (Task-spec-contract output)

**Task title:** A3R — Delete the public `.github/workflows/cluster-smoke.yml` (D3 = relocate; user preservation confirmation received)

**Read order (mandatory, all normative and read-only for you):** 1) `devlog/changes/public-repo-hygiene/FEATURE.md` (umbrella §5–§7); 2) `devlog/changes/public-repo-hygiene/slices/A3-private-infra-scrub.md` (the parent slice — its blocked workflow sub-step is now unblocked); 3) repository-root `AGENTS.md` — the **rewritten public-tone text**; your run is one of the first post-rewrite observations (R2), so your evidence must show you worked squarely within it. On any contradiction: stop and ask via your ask channel.

**Target (five-part contract):** delete exactly the file `.github/workflows/cluster-smoke.yml`. **Untouchable:** everything else — in particular `.github/workflows/verify.yml` (public CI, P1: never weakened, never removed) and `devlog/changes/repo-professionalization/labs/ci/cluster-smoke.yml` (the preserved copy — D2-bound relocation content; NEVER delete, edit, or even touch it).

**Precondition (must hold before you delete; user decision recorded 2026-09-24):** the user confirmed the preservation of the copy staged at `labs/ci/cluster-smoke.yml`. Verify it yourself first: `cmp .github/workflows/cluster-smoke.yml devlog/changes/repo-professionalization/labs/ci/cluster-smoke.yml` must be clean and both `shasum` values must equal `b1295cd3f93b421b5afcc90ae6ab0ae8d0684fcd`. If (and only if) that precondition holds, delete the public file. If it does not hold: stop, change nothing, report `--outcome failed` with the observed checksums.

**Change:** remove the public private-self-hosted workflow leg (its capability is preserved for the future cbse-labs repo in the staged copy; relocation runbook: `devlog/changes/repo-professionalization/labs/cluster-smoke-relocation.md`).

**Constraints:** no cluster operations, no network, no commits (P6); scope discipline — nothing beyond the named file; the rewritten `AGENTS.md` requires no cluster tier for this change; secrets hygiene as umbrella §5.

**Observable acceptance (all echoed in your worker_done; the manager re-runs each):**
1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat in your executive summary; mismatch → stop, change nothing, `--outcome failed`).
2. Precondition proof: the `cmp`-clean check and the shasum pair echoed BEFORE deletion.
3. Containment: `git status --short` shows exactly ` D .github/workflows/cluster-smoke.yml` attributable to you (plus expected co-resident entries of the in-flight A2R wave-mate under `test/compat/`/`hack/`, and the settled-uncommitted Package-A change set already present — zero other entries attributable to you).
4. Post-deletion proof: `ls .github/workflows/` → only `verify.yml`; the preserved copy still present and byte-identical (`shasum devlog/changes/repo-professionalization/labs/ci/cluster-smoke.yml` = `b1295cd3f93b…`); `LC_ALL=C grep -rnE 'self-hosted|cbse-k3s' -- .github` → rc=1, no output.
5. Run the R3 link-check recipe (umbrella §7) over the full tree → no new BROKEN lines.
6. Run `make test-fast` at completion and record rc=0 (charter §8 evidence; first-observations under the rewritten `AGENTS.md` close the R2 record). Echo the trailing rc line. On any failure: `--outcome failed` with exact output — never work around, never weaken a test.
