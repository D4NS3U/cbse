# IMPLEMENTATION_HANDOFF.md — public-repo-hygiene (Package A)

Routing evidence for Package A of the [repo-professionalization charter](../repo-professionalization/PROBLEM.md). The manager updates this file after every settlement. Slice details: [FEATURE.md](FEATURE.md) · [slices/](slices/). Status vocabulary: `complete` / `incomplete` / `verification-blocked`, evidence tied to a revision or recorded worktree state.

## Program state

- Charter: accepted; user notified 2026-09-24 that the repository **is already public** (D4 resolved: `github.com/D4NS3U/cbse`; local history fully pushed at tip `9f01e82`). The public surface shows the pre-professionalization state until the user lands the P6 commit set; the verified-uncommitted delta is 204 files (+2688/−1847). Packages B/C gate-locked, untouched (B additionally needs M1 + D5).
- Gates: D1 — alpha2/alpha3 confirmed; `test/compat/eds-sm/` + root `hack/` pending user confirmation (sub-steps blocked). D3 — relocate; public `cluster-smoke.yml` deletion blocked on the user's preservation confirmation. D2/D4/D5/D6 informational.
- Wave plan: Wave 1 = A1 ∥ A2 → Wave 2 = A3 ∥ A4 (R2: A3 serialized behind Wave 1) → Follow-up wave = A2R ∥ A3R (user confirmations received 2026-09-24). **A1–A4 and both follow-up deletions settled `complete` and reviewer-verified on 2026-09-24.** D1 residual deletions executed (eds-sm + hack, with two licensed single-line doc reference fixes); D3 public `cluster-smoke.yml` deletion executed (preservation precondition verified: labs copy shasum `b1295cd3f93b…`). Pending: user checkpoint commits; A4 labs review → cbse-labs creation (D2); the M0 external-reviewer fresh-clone dry-run.

## Slice A1 — OSS staples

- State: `complete` (settled 2026-09-24; dispatch `ctx_ce395298e43b`, delivery `msg_2cd28e9d8a17`, outcome `succeeded`).
- Attestation: `ai.forge/qwen3.8-27b-nvfp4` (verbatim in executive summary and E1).
- Deliverables: product README (Quickstart line 66, Architecture line 23, verify.yml CI badge template with `<your-org>` placeholder, environment-gated testing section, no `helm install` claims, no `trans-mock`/`alpha3` remnants), `CONTRIBUTING.md`, Keep-a-Changelog `CHANGELOG.md` (seeded, `[Unreleased]` present), `SECURITY.md` with explicitly marked placeholder disclosure contact.
- Evidence (manager re-ran every recipe from a fresh shell): private-string scan over the four files rc=1 clean ✔; full-tree link check shows zero broken links attributable to A1 (only the four known pre-existing broken links in A3-owned `docs/CBSE_TESTING_GUIDE.md` remain, routed to Wave 2) ✔; `make test-fast` rc=0, zero FAIL lines ✔ (the 12 stderr `fatal: could not open api/alpha[23]…` lines tracked as P6 commit-transient, distinct from A2's identical finding).
- LICENSE verification (report-only, nothing edited): DEVIATION — the canonical `APPENDIX: How to apply the Apache License` block missing (190 vs ~201 lines); 133/155 Go files lack Apache headers; attribution inconsistency (LICENSE 'Copyright 2026 Daniel Seufferth' vs stock headers 'Copyright 2025.'). All three routed as open user decisions (P3). SECURITY.md contact and README `<your-org>` URLs are also user decisions (publicization, Package B).

## Slice A2 — dead trees deletion

- State: `complete` (settled 2026-09-24; dispatch `ctx_6be1b0e61651`, delivery `msg_adf6b72c66c3`, outcome `succeeded`).
- Attestation: `ai.forge/qwen3.8-27b-nvfp4` (verbatim in executive summary and evidence block).
- Evidence: manager re-ran every recipe independently from a fresh shell — (1) `ls experiment-operator/api` = `alpha4` only; (2) reference scan `grep -rnE 'api/alpha[23]'` over module trees + `docs` + devlog index = rc=1 clean; (3) `docs/COMPONENT_DESIGN_GOALS.md` line 406 re-pointed to `../devlog/changes/README.md` with the prescribed parenthetical (link-only fix; content refresh untouched); (4) licensed change: `experiment-operator/PROJECT` — exactly the alpha2/alpha3 `resources:` blocks removed, all other lines byte-identical (licensed via the A2 ask `msg_880a90edbdfe`; rationale: user D1 "code changes necessary for this deletion"; `verify-generated.sh` uses PROJECT only as a copy source — reviewer-verified); (5) blocked items `test/compat/eds-sm/` + root `hack/` present and untouched; (6) `make test-fast` rc=0 re-run by the manager with zero FAIL lines (the 12 `fatal: could not open ...` strings are benign `build-images.sh` source-fingerprint noise over deleted-uncommitted files — P6 transient, disappears at commit).
- Worker report lines routed onward: four pre-existing broken links inside `docs/CBSE_TESTING_GUIDE.md` (→ deleted alpha3-era sources: `internal/controller/simulationexperiment_controller.go`, `internal/core/scenario_manager.go`, `internal/nats/eds_com.go`, `internal/nats/trans_com.go`) — inside A3's ownership; A3's "all relative links resolve" requirement covers them, and the manager checks exactly those four at A3 settlement.

## Slice A3 — private-infrastructure scrub

- State: `complete` (settled 2026-09-24; dispatch `ctx_eafd39d068b5`, delivery `msg_97b1709ff531`, outcome `succeeded`).
- Attestation: `ai.forge/qwen3.8-27b-nvfp4` (verbatim in executive summary and E1).
- Landed: Makefile — private registry default removed, documented environment-provided `CBSE_REGISTRY` (no default), exit-2 guards in exactly the three consuming targets (`publish-test-images`, `test-smoke`, `test-e2e-retained`), help switched to `<your-registry>` placeholders; `make test-fast`/`help` unguarded and runnable with the variable unset (reviewer-proven). `AGENTS.md` — public-tone rewrite preserving the tier contract verbatim-equivalent, genericized cluster safety with the identify-and-refuse rule intact, placeholder commands, P3 distinction embedded (R2 atomicity: single wave; landed contract recorded in the worker's summary). `docs/CBSE_TESTING_GUIDE.md` — re-anchored; the four known pre-existing broken links fixed (all were in this file); stale alpha3-era claims replaced with verified current facts.
- **Licensed R4-letter deviation (user decision 2026-09-24, option a):** `test/harness/test-harness.sh:122` invariant re-pointed from pinning the private default's presence to the no-value form `grep -Fqx 'CBSE_REGISTRY ?=' "${root}/Makefile"` — exactly one line changed (manager-verified diff), whole-line exactness preserved, P1 honored (invariant re-pointed at equal strength; any re-added private default fails the check). R4 spirit preserved: all scripts live, private tier functional (build-images.sh/smoke.sh internal fallbacks untouched until C1), `test-fast` green throughout.
- Evidence (manager re-ran every recipe from a fresh shell): private-string scan over Makefile/AGENTS.md/CBSE_TESTING_GUIDE.md rc=1 clean ✔; `cluster-smoke.yml` byte-identical `b1295cd3f93b…` before/after, `.github/` and `verify.yml` untouched ✔; guard dry-runs (unset → exit 2; set → dry-run rc 0) ✔; full-tree link check zero BROKEN ✔; wave-final `make test-fast` rc=0, zero FAIL lines, "Harness self-tests passed" under the re-pointed invariant ✔.
- Routed to Package C (report-only, untouched per R4/§6): out-of-partition private strings in `test/e2e/README.md` (kubeconfig path, private API-server URL, registry prefix, stale flat-layout description) and harness internals (`smoke.sh:22`, `build-images.sh:14,40`, `preflight.sh:19,47`).

## Slice A4 — cbse-labs preparation

- State: `complete` (settled 2026-09-24; dispatch `ctx_7479f72256be`, delivery `msg_8192ce6dfae6`, outcome `succeeded`).
- Attestation: `ai.forge/qwen3.8-27b-nvfp4` (verbatim in executive summary and E1).
- Deliverables (all under `devlog/changes/repo-professionalization/labs/`): `inventory.md` (MANAGER.md, `agents/**`, `devlog/**` wholesale incl. program governance, `docs/project-status.md`, `artifacts/` note), `move-list.md` (ordered D2 runbook + charter §5 cbse-labs skeleton + stays-public list + post-move manager orientation), `env-provider.template.sh` (placeholder-only provider with an unfilled-placeholder guard), `cluster-smoke-relocation.md` (D3 runbook; explicit "public file NOT deleted until user confirms preservation" condition), and the byte-identical preserved copy `ci/cluster-smoke.yml`.
- Evidence (manager re-ran the static recipes from a fresh shell): deliverable list exact (5 files) ✔; `cmp` clean and `shasum` pair equal (`b1295cd3f93b…`, 63 lines / 1839 bytes) with `.github/` untouched ✔; template-hygiene grep rc=1 over the three labs files ✔; R1 integrity (MANAGER.md clean; the modified PROBLEM.md + untracked prompts/ pre-date the session — prompts/ is user-owned, PROBLEM.md's Status line is the manager's charter-keeping edit) ✔; labs link check zero BROKEN ✔. `make test-fast` rc=0 recorded by the worker (E8); the manager's wave-final reviewer `test-fast` re-run lands after A3 settles (avoiding a race with A3's in-flight Makefile edits).
- R2 observation note: A4 started under the PRE-A3-rewrite `AGENTS.md` (worker-verified at start; co-dispatched in the same wave), so the post-A3 observation duty cannot ride on A4 — it rides the next dispatch that STARTS after A3 settles (expected: the preservation-deletion follow-up, gated on the user's confirmation).

## Slice A2R — residual deletions (eds-sm + hack)

- State: `complete` (settled 2026-09-24; dispatch `ctx_1c63f2257d91`, delivery `msg_293ea9b0c1cb`, outcome `succeeded`).
- Attestation: `ai.forge/qwen3.8-27b-nvfp4`.
- Landed: `test/compat/eds-sm/**` deleted (8 tracked files) plus the emptied `test/compat/` and root `hack/` (filesystem-only); two delegation-licensed single-line deletion-forced fixes: `docs/CBSE_TESTING_GUIDE.md:69` (dir-map entry for the deleted directory) and `test/README.md:8` (compat bullet). License rationale: same necessity class as A2's CDG fix, under the user's 2026-09-24 deletions confirmation; ask-channel ruling `msg_ee8cef2db147`.
- Evidence (manager re-ran from a fresh shell): `test/compat` and `hack` gone ✔; `test/README.md` diff = exactly one removed bullet ✔; the guide's dir-map entry for eds-sm removed inside A3's cumulative wave diff ✔; `git grep -n 'compat/eds-sm'` → zero non-devlog hits, exactly 7 historical devlog/changes lines ✔; make test-fast worker rc=0 recorded + manager wave-final rc=0 ✔; R3 link check clean ✔; containment: exactly the 8 ` D eds-sm` lines + 2 ` M` doc files attributable ✔.

## Slice A3R — public cluster-smoke.yml deletion

- State: `complete` (settled 2026-09-24; dispatch `ctx_fd76dc936a65`, delivery `msg_167c68bd9f98`, outcome `succeeded`).
- Attestation: `ai.forge/qwen3.8-27b-nvfp4`.
- Landed: `.github/workflows/cluster-smoke.yml` deleted under the user's preservation confirmation (D3 = relocate). Precondition verified by worker and manager: `cmp` clean, both shasums `b1295cd3f93b…`; preserved copy intact at `labs/ci/cluster-smoke.yml`.
- Evidence (manager re-ran from a fresh shell): `.github/workflows/` contains only `verify.yml` ✔; `grep self-hosted|cbse-k3s -- .github` rc=1 ✔; preserved copy shasum re-verified ✔; zero staged/unmerged entries repository-wide ✔ (the worker reported an external file-watcher auto-staged its deletion twice and neutralized via index-only `git reset` — end state verified unstaged and reviewable) ✔; R3 link check clean ✔; make test-fast worker rc=0 recorded + manager wave-final rc=0 ✔.

## Slice L1 — license-header pass + generator boilerplate + CDG licensing section

- State: `complete` (settled 2026-09-24; dispatch `ctx_6dd952916669`, delivery `msg_2d71230c2ea4`, outcome `succeeded`). User decision 2026-09-24: uniform Apache-2.0 short-form headers across eligible sources; holder `Copyright 2025-2026 Daniel Seufferth` (from the LICENSE copyright line).
- Attestation: `ai.forge/qwen3.8-27b-nvfp4`.
- Landed: canonical `//` header in every tracked worktree-present `*.go` (133 additions + 15 stock-form normalizations), all 12 live `*.sh` (after shebang), all 17 live `*.py` (before docstrings); `experiment-operator/hack/boilerplate.go.txt` rewritten to the canonical form and `zz_generated*.go` re-derived via `make -C experiment-operator generate` (never hand-edited); `docs/COMPONENT_DESIGN_GOALS.md` gained one additive "Licensing your components" section (18 added lines, 0 removed); five `//go:build e2e` constraints relocated directly beneath the headers (spec-mandated).
- Evidence (manager re-ran from a fresh shell): completeness sweep over go/sh/py → zero missing; stock `Copyright 2025.` form gone; boilerplate canonical; `LICENSE` untouched; regenerate idempotent (no new deltas); manager audit over `git diff --diff-filter=d` excluding header vocabulary reduced to header-internal separators, the spec-mandated tag relocations, and A3's already-licensed harness line — 179 modified files, +2492/−258, header-only; manager `make test-fast` rc=0, zero FAIL, harness self-tests green (includes verify-generated, race suites, operator envtest, translator Python conformance).
- Routed for Package C1 (worker-reported, manager-corrected routing): private strings remaining in `test/e2e` Go tests and harness internals are C1-class cluster-tier remnants (not A3's settled scope — A3 owned Makefile/AGENTS.md/CBSE_TESTING_GUIDE only). Untouched per R4; none of L1's added lines contain denied strings.
- Still OPEN (user, unchanged): the LICENSE missing-APPENDIX finding — the user licensed only the header pass; the canonical appendix block remains a pending user decision (P3).

## Slice S2 — SECURITY channel + public org fill-in

- State: `complete` (settled 2026-09-24; dispatch `ctx_dae7b2f50042`, delivery `msg_9f64def67f30`, outcome `succeeded`). User decisions 2026-09-24: GitHub built-in private vulnerability reporting as disclosure channel; public org stays `D4NS3U`.
- Attestation: `ai.forge/qwen3.8-27b-nvfp4`.
- Landed: `README.md` — all four `<your-org>` placeholders resolved (badge URL and clone URL to `github.com/D4NS3U/cbse`, caption/comment reworded truthfully); `SECURITY.md` — placeholder disclosure contact replaced with the GitHub channel wording ("accepted via" phrasing; no fabricated emails; no claim the feature is already enabled).
- Evidence (manager re-ran from a fresh shell): `<your-org>` and `PLACEHOLDER` greps rc=1 across both files; resolved lines verified (`badge.svg` URL, `git clone https://github.com/D4NS3U/cbse.git`); channel wording inspected; links resolve; containment: exactly `README.md` + `SECURITY.md`.
- User-side companion (external action, user-owned): enable GitHub's Private vulnerability reporting toggle in the repository settings (Settings → Code security and analysis). Reported to the user in the manager's final report. **RESOLVED 2026-09-24: the user enabled the toggle in the GitHub UI** — the S2 channel wording is now fully backed.

## Slice L2 — LICENSE appendix completion

- State: `complete` (settled 2026-09-24; dispatch `ctx_cf52d24667c6`, delivery `msg_d0f7419d80c5`, outcome `succeeded`). User decision 2026-09-24: append the missing canonical APPENDIX block (P3: attribution untouched).
- Attestation: `ai.forge/qwen3.8-27b-nvfp4`.
- Landed: the canonical 8-line `APPENDIX: How to apply the Apache License to your work.` heading + instructions paragraph inserted between `END OF TERMS AND CONDITIONS` (line 176) and the preserved applied `Copyright 2026 Daniel Seufferth` notice (now line 187); the applied notice stands as the appendix's filled-in instance.
- Evidence (manager re-ran from a fresh shell): `git diff LICENSE` = exactly one additive hunk +9/−0, byte-exact to the block text in the dispatch spec; `grep -n` ordering END(176) → APPENDIX(178) → Copyright(187) ✔; `wc -l` 199 ✔; attribution lines byte-identical ✔; containment = ` M LICENSE` only ✔. The worker also correctly noted a manager recipe flaw (`git diff | grep -c "^-"` counts the `--- a/LICENSE` header) — numstat is the trustworthy form; its report used it.
- The LICENSE findings list from A1 is now FULLY resolved: (a) appendix block ✔ appended; (b) Go headers ✔ (L1); (c) attribution harmonized to the canonical `Copyright 2025-2026 Daniel Seufferth` header form with the LICENSE applied notice preserved ✔.

## Post-A3 observation (R2 confirmation)

- **CLOSED 2026-09-24.** Both follow-up dispatches (A2R `ctx_1c63f2257d91`, A3R `ctx_fd76dc936a65`) started after A3's settlement and worked under the rewritten `AGENTS.md`; both recorded `make test-fast` rc=0 receipts, and the manager's wave-final re-run (rc=0, zero FAIL, "Harness self-tests passed") confirms workers still satisfy the rewritten contract.
