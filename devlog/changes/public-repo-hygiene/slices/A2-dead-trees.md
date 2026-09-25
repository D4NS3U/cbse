# Slice A2 — Dead trees deletion

Normative and self-contained for its worker. Read order and global constraints: [../FEATURE.md](../FEATURE.md) §5 (P1–P8 verbatim, operating rules, attestation requirement). This slice spec extends, never overrides, the umbrella. All three documents (umbrella, this slice, root `AGENTS.md`) are read-only for you.

## Target

Exactly: delete `experiment-operator/api/alpha2/**` and `experiment-operator/api/alpha3/**`; fix the single `docs/COMPONENT_DESIGN_GOALS.md` reference that the alpha3 deletion forces (the list item at line ~406: `- [`alpha3` component fields](../experiment-operator/api/alpha3/simulationexperiment_types.go)`). **Only those.** Everything else in the repository is untouchable — including these two explicitly **blocked** items that are *not* licensed for deletion this run:

- `test/compat/eds-sm/` — **blocked on D1** (user has not confirmed it; do not delete, do not edit, do not reference-delete).
- root `hack/` — **blocked on D1** (same rule; note: it is empty and untracked; also **do not confuse** it with `experiment-operator/hack/`, which is live and referenced by the operator Makefile's deepcopy target and stays).

## Change

1. Delete the retired, unreconciled API trees: `rm -r experiment-operator/api/alpha2 experiment-operator/api/alpha3`. Verified live state you may rely on: these trees contain only `groupversion_info.go`, `simulationexperiment_types.go`, `zz_generated.deepcopy.go` each; there are zero live imports (grep-clean outside their own dirs); the active CRD is generated exclusively from `./api/alpha4/...` (`experiment-operator/Makefile`, `crd:` invocation), so `verify-generated` cannot be affected.
2. Fix the dangling doc reference in `docs/COMPONENT_DESIGN_GOALS.md`: the list item pointing at `../experiment-operator/api/alpha3/simulationexperiment_types.go` must point instead at a retired-API notice — the truthful source already in the tree is `devlog/changes/README.md`'s index (alpha4-only statement, "alpha2 was retired by the alpha4 cutover" line) — use the last-known-good target: relink to `../devlog/changes/README.md` with a short parenthetical `(alpha2/alpha3 retired; alpha4 is the only served and stored version)`. **Exactly this one line changes** in that file; any alpha3-era framing elsewhere in that document stays (its content refresh is a later editorial slice — out of scope).

## Constraints

- **P1 verbatim** binds absolutely: tests must stay green — this slice is a Go-tree change, so `make test-fast` (which includes `make verify-generated`, `go vet`, compile gates, race suites, the operator envtest) **is mandatory evidence and must be rc=0 after your deletions**. If anything fails, you must not work around it; report `--outcome failed` with the failing test's exact output and your analysis (test integrity: never weaken/delete/skip a test to conceal a failure).
- **No private-string scope creep (R4):** your acceptance recipes are scoped **only** to your deletions and the doc line you fix. You are not judged on `AGENTS.md`/`Makefile`/`CBSE_TESTING_GUIDE` private strings — those are A3's waves, still landed or in flight; leave them alone.
- **No edits anywhere else.** Discovered gaps → report lines in `worker_done`, never ownership handwaves.
- The harness self-tests inside `test-fast` exercise private-cluster scripts (R4); they keep passing because you touch no harness file.
- No cluster operations, no `make test-smoke`, no registry/network operations.
- If you find anything importing the deleted trees that the pre-flight evidence missed: stop and report via your ask channel; do not fix beyond your partition (this would be a `verification-blocked` or ask-channel event, never a quiet workaround).

## Ownership

Sole owner of `experiment-operator/api/alpha2/**`, `experiment-operator/api/alpha3/**` (as deletions) and the one referenced line in `docs/COMPONENT_DESIGN_GOALS.md`. The parallel worker owns A1's partition (README root + three new staples files); you may not touch those, nor they yours.

## Observable acceptance

Run and echo all of these in `worker_done` (the manager re-runs each independently from a fresh shell):

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat in your executive summary; mismatch → stop, change nothing, `--outcome failed`).
2. Containment: `git status --short` shows exactly: deletions under `experiment-operator/api/alpha2/…` and `experiment-operator/api/alpha3/…` (` D` lines), plus the single ` M docs/COMPONENT_DESIGN_GOALS.md` line — and, co-resident but not yours, only A1's four staple files if it happens to be in flight. Zero other entries. `test/compat/eds-sm/` and root `hack/` untouched (echo `ls test/compat/ hack/` proving presence).
3. Deletion proof: `ls experiment-operator/api` → only `alpha4`. Reference scan: `LC_ALL=C grep -rnE 'api/alpha[23]' -- experiment-operator scenario-manager component-templates test docs devlog/changes/README.md` → no output except devlog **historical** mentions under `devlog/changes/**` (feature history is allowed and expected there — devlog entries are process history, not live references; `docs/COMPONENT_DESIGN_GOALS.md` must be among the clean).
4. Doc fix proof: `grep -n "alpha3\` component fields" docs/COMPONENT_DESIGN_GOALS.md` → line shows the new target `../devlog/changes/README.md`; the R3 link-check recipe (umbrella §7) over `docs/*.md` → no `BROKEN:` lines pointing into `api/alpha[23]`.
5. Build+test proof: `make test-fast` rc=0 — echo the final command echo `rc=0` from your run (the run prints per-phase output; your report must include the last ~5 lines as the receipt).

Completion protocol: `worker_done` with a three-sentence executive summary, both lifecycle IDs (task + dispatch), explicit `--outcome succeeded|failed`, the verbatim attestation line, and the five evidence blocks above.
