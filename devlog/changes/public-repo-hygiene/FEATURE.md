# FEATURE.md — Package A: Public repo hygiene (`public-repo-hygiene`)

**Program:** repo-professionalization (charter: [`../repo-professionalization/PROBLEM.md`](../repo-professionalization/PROBLEM.md), all §-references below point into it).
**Normative for every worker dispatched under it. Read-only for workers.** On any contradiction between this umbrella, your slice, and reality: stop and ask via the ask channel rather than resolving by opinion.

## 1. Title and mission

Package A converts the dev-heavy repository into a professional, public-ready product surface in four ownership-disjoint slices, **without changing runtime behavior, module boundaries, or test tiers** (charter P8/P1): A1 — OSS staples (README product rewrite, `CONTRIBUTING.md`, `CHANGELOG.md`, `SECURITY.md`, LICENSE verification); A2 — dead-tree deletion (`experiment-operator/api/alpha2/`, `api/alpha3/`, plus the one doc reference those deletions force); A3 — private-infrastructure scrub of tracked defaults and agent-facing prose (`Makefile` registry default, `AGENTS.md`, `docs/CBSE_TESTING_GUIDE.md`); A4 — cbse-labs preparation manifest (new files under `../repo-professionalization/labs/` only; the moves themselves are D2, user-executed). The manager is the evidence reviewer, never the implementer; the user owns every git operation (P6). Future work (out of this package): Package B release pipeline, Package C dev-box sunset, the `COMPONENT_DESIGN_GOALS.md` content refresh (charter §6 note) — all gate-locked and untouched here.

## 2. Scope

- **In scope:** exactly the file-ownership partition in §6 (below). Nothing else.
- **Out of scope (charter §13):** framework feature development; any operator/SM/translator behavior, API, or runtime-contract change; repo splitting into submodules (R6); `MANAGER.md`, `agents/**`, `devlog/**` relocations themselves (D2, user-executed — A4 manifests them only); `docs/COMPONENT_DESIGN_GOALS.md` content refresh beyond the single dead-reference fix owned by A2; Package B/C work (B/C gates closed; do not pre-write them, charter §12.4).
- **Breaking changes:** A2 deletes two retired, unreconciled API source trees (`api/alpha2`, `api/alpha3`). No schema/CRD change: the active CRD is generated from `./api/alpha4/...` only (`experiment-operator/Makefile` `crd:` invocation), and no Go code imports the deleted trees (verified at decomposition time). All generated artifacts and both test tiers must remain green (`P1`).
- **Compatibility boundaries:** Kubernetes >= 1.30; alpha4-only API; the software performs no feature-gate changes.
- **Notably NOT in scope for A3 (charter R4):** `test/harness/**` — smoke/lease/cleanup scripts stay untouched until Package C; A3 changes only defaults, docs, and agent-facing prose.
- **Notably NOT in scope for anyone (charter R1):** `MANAGER.md`, the charter, `devlog/` structure, `agents/**` — no slice deletes, moves, or edits them. A4 only *lists* them for the user's D2 move.

## 3. Workflow decisions (charter §6, A-relevant rows only)

| Concern | Decision for this package | Owning slice / gate |
|---|---|---|
| README.md | Product rewrite: what/why, architecture, quickstart (source-install now; chart install forward reference), CI badge template, docs links | A1 |
| OSS staples | Add `CONTRIBUTING.md`, `CHANGELOG.md` (Keep-a-Changelog, seeded from `git log` at feature granularity), `SECURITY.md` (structure with explicit placeholder contact, reported as open user decision) | A1 |
| LICENSE | Verify explicit Apache-2.0 text + consistent `Apache License` headers; **deviations are reported to the user, never edited** (P3) | A1 |
| `experiment-operator/api/alpha2/`, `api/alpha3/` | Delete (retired, unreconciled); zero live imports; CRD derives from alpha4 only | A2 — **D1 confirmed** |
| `docs/COMPONENT_DESIGN_GOALS.md` dead alpha3 reference | Fix at deletion time: point at the retired-API notice, not the deleted file; **link fix only — no content refresh** | A2 |
| `test/compat/eds-sm/`, root `hack/` | Deletion desired (charter §6) but **not yet user-confirmed** — sub-steps `blocked` on D1; do not touch | A2 (blocked sub-step) |
| `Makefile` private registry default | Default value removed; `CBSE_REGISTRY` documented as required, environment-provided, never embedded (guard inside the targets that need it; `make test-fast` path unaffected) | A3 |
| `AGENTS.md` | Public-tone rewrite preserving the tier contract and cluster-safety rules genericized (R2 atomicity: land in one wave, handoff records the new contract, the next dispatch is observed under it) | A3 |
| `docs/CBSE_TESTING_GUIDE.md` | Re-anchored, public tone, links resolve (R3) | A3 |
| `.github/workflows/cluster-smoke.yml` | **D3 = relocate.** A4 stages the byte-identical preserved copy in its labs deliverables; the public file is deleted only after the user confirms the preservation — a **blocked sub-step of A3** pending that confirmation, executed by a follow-up dispatch | A3 (blocked) + A4 (staging) + user gate |
| `MANAGER.md`, `agents/**`, `devlog/**`, `docs/project-status.md` | Move to cbse-labs — **user-executed at D2**; A4 prepares the manifest only (R1) | A4 |
| Harness scripts (`smoke.sh`, `acquire-lock.sh`, `registry-cleanup.sh`, lease/retention flags) | Keep, untouched, until C1 (R4) — the private cluster tier keeps working all through Package A | nobody |
| `.github/workflows/verify.yml` | Public CI running `make test-fast` on `ubuntu-latest` — keep, never weaken (P1) | nobody |

## 4. Design patterns (Gang of Four)

**Non-applicable, with justification as required by the authoring contract.** This package introduces no new object structure: it is documentation authorship, file deletion, tracked-default/prose scrubbing, and manifest preparation. No interfaces, packages, types, seams, or behavioral components are created or reshaped, so every GoF pattern would be speculative — exactly what the repo's engineering values forbid. The GoF discipline (charter §6 note) remains binding for any future slice that changes code structure (e.g. the `COMPONENT_DESIGN_GOALS.md` refresh slice and Packages B/C).

## 5. Global constraints (P1–P8 verbatim, plus operating rules)

The charter's operating principles, verbatim:

- **P1 — Tests survive publicly or not at all.** No test, tier, or assertion is deleted before its public replacement is green. `test-fast` and `verify-generated` are never removed or made private-only.
- **P2 — Private platform out, not tests out.** The K3s dev box, Harbor conventions, self-hosted runner, and lock/lease choreography leave the public repository; their capability is transferred (public ephemeral clusters, public registry, release validation) before the private instances are dropped.
- **P3 — Attribution stays.** Institution name in LICENSE, copyright headers, and paper references is legitimate attribution and must survive the scrub. Operational infrastructure (IPs, registry names, runner labels, kubeconfig paths, hostnames) must not. A slice spec must state this distinction verbatim before scrubbing.
- **P4 — Working contracts stay self-consistent.** `AGENTS.md` is normative for every worker while it works; the slice that rewrites it must land it in a state that is simultaneously true for subsequent runs, and the update is recorded in the slice handoff. `MANAGER.md` is the Manager's own contract: the Manager must not dispatch a slice that deletes it (see R1).
- **P5 — No secrets exposure.** Before any publicization gate, a dedicated audit slice must prove by recipe (grep patterns + review) that no tracked file contains credentials, tokens, private kubeconfigs, or registry auth material. Known good state: registry auth arrives via `CBSE_REGISTRY_AUTH_FILE` externally; harness files must not grow private values during M0.
- **P6 — User owns git and all external actions.** Workers leave the worktree reviewable and create no commits. The user performs all commits, tags, pushes, repository creations, and registry/publication actions, advised by Manager reports. Every external-action requirement becomes an explicit user decision gate in the Manager's report.
- **P7 — Product-first surface.** Everything the simulation expert needs (README quickstart, install path, extension templates, cluster requirements, docs) is first-class; everything else is moved out, deleted, or documented for contributors in `CONTRIBUTING.md`.
- **P8 — No re-architecture during tidying.** This program changes packaging, CI, docs, and placement. It does not change runtime behavior, module boundaries, the monorepo topology (submodules were reviewed and rejected: `git` submodules do not serve Helm and the monorepo protects CRD single-sourcing and coherent image-set versioning), or the alpha4 contracts.

Operating rules for every Package-A worker:

- **Read order:** this umbrella → your slice → repository `AGENTS.md` (current text; it governs your run — the rewrite lands mid-program per R2) → then code/files. All three are normative and read-only. `MANAGER.md` and the charter are context; you never edit any of them.
- **Obey `AGENTS.md`:** the repository test contract and cluster-safety rules are incorporated by reference. Cluster safety as it binds you concretely: no `kubectl`, no cluster contact, no registry pushes, no `KUBECONFIG` use — Package A needs no cluster operations; the smoke tier is never invoked by your slice.
- **Secrets hygiene:** never expose credentials, credential paths, Secret payloads, tokens, or decoded auth material in commands, logs, reports, or artifacts; never echo `CBSE_REGISTRY_AUTH_FILE` contents. Template placeholders only — never invent private values.
- **Test integrity:** never weaken, delete, skip, or rewrite a test to conceal a failure; diagnose at the source; flag suspected real bugs instead of fixing them when your spec forbids it.
- **Scope discipline:** implement exactly your partition; no unrelated cleanup, no speculative abstractions, no edits to "nearby" files. A discovered gap becomes a report line in your `worker_done`, never an ownership handwave.
- **Verification honesty:** a slice is complete only after its verification gate passed; report unavailable mandatory verification as `verification-blocked`, not as success. Status vocabulary: `complete` / `incomplete` / `verification-blocked`.
- **No commits (P6):** leave every change in the working tree; the user commits.
- **Runtime attestation (mandatory):** at your first checkpoint run `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` and repeat the verbatim output line in your `worker_done` executive summary. If the line is not `ai.forge/qwen3.8-27b-nvfp4`, stop immediately, change nothing, and report `--outcome failed` with the observed line.
- **Denied content (P3/P5 exact strings):** the private-operations scrub targets `192.168.101.245:6443`, `/home/d4ns3u/.kube/config`, `registry.unibw.de/i31bdase/cbse-test` (as a baked-in default), `runs-on: [self-hosted, linux, x64, cbse-k3s]` — never institution attribution (LICENSE, copyright headers, paper references) and never anything in the harness scripts (R4).

## 6. Slices and the file-ownership partition (canonical)

Ordering: **Wave 1 = A1 ∥ A2** (parallel, ownership-disjoint) → **Wave 2 = A3 ∥ A4** (after Wave 1 fully settles; A3 rewrites the working contract, R2; A4 is pure new-file creation). `Blocked` sub-steps are authored, marked, and dispatched around — never approximated, never decided by a worker.

| Path | Slice | Change |
|---|---|---|
| `README.md` | A1 | product rewrite |
| `CONTRIBUTING.md` (new), `CHANGELOG.md` (new), `SECURITY.md` (new) | A1 | create |
| `LICENSE` | A1 | verify only; report deviations; never edit |
| `experiment-operator/api/alpha2/**` | A2 | delete (D1 confirmed) |
| `experiment-operator/api/alpha3/**` | A2 | delete (D1 confirmed) |
| `docs/COMPONENT_DESIGN_GOALS.md` | A2 | the single `.github/../`-relative alpha3 reference fix (line ~406); content refresh prohibited |
| `test/compat/eds-sm/**` | nobody | **blocked** on D1 — untouched |
| root `hack/` | nobody | **blocked** on D1 — untouched (distinct from `experiment-operator/hack/`, which stays) |
| `Makefile` | A3 | registry-default removal + documented required var + guard |
| `AGENTS.md` | A3 | public-tone rewrite, one atomic wave |
| `docs/CBSE_TESTING_GUIDE.md` | A3 | re-anchored public tone |
| `.github/workflows/cluster-smoke.yml` | follow-up | **blocked** on the user's preservation confirmation (D3 = relocate; A4 preserves first) |
| `devlog/changes/repo-professionalization/labs/**` (new) | A4 | create, new files only |
| `.github/workflows/verify.yml`, `test/harness/**`, `experiment-operator/hack/**`, `MANAGER.md`, `agents/**`, `devlog/**` (rest), `docs/project-status.md`, everything else | nobody | untouchable in Package A (P1/R4/R1) |

Slice specs: [`slices/A1-oss-staples.md`](slices/A1-oss-staples.md) · [`slices/A2-dead-trees.md`](slices/A2-dead-trees.md) · [`slices/A3-private-infra-scrub.md`](slices/A3-private-infra-scrub.md) · [`slices/A4-cbse-labs-preparation.md`](slices/A4-cbse-labs-preparation.md). Dispatch prompts (one per worker actually launched): [`prompts/`](prompts/).

## 7. Shared verification gates (re-runnable recipes)

Every slice's acceptance recipe, plus the manager's own reviewer evidence, uses these exact commands:

**Private-string scan** (charter §4/§11 items; scope = the slice's owned paths):

```sh
LC_ALL=C grep -rnE '192\.168\.|registry\.unibw\.de|i31bdase|cbse-k3s|/home/[^ )`]*\.kube|ai\.forge' -- <paths...>
```

Clean state: grep exits 1 with no output. A hit is a finding the slice must remove (or, in A4's preserved-copy case only, explicitly declare and justify as D2-bound content).

**R3 documentation-link check** (run for the tree: `README.md`, `CONTRIBUTING.md`, `CHANGELOG.md`, `SECURITY.md`, `docs/*.md`, `devlog/changes/README.md`):

```sh
LC_ALL=C
for f in README.md CONTRIBUTING.md CHANGELOG.md SECURITY.md docs/*.md devlog/changes/README.md; do
  [ -f "$f" ] || continue
  dir=$(dirname "$f")
  grep -oE '\]\([^)]+\)' "$f" | sed -E 's/^\]\(//; s/\)$//' \
  | while IFS= read -r link; do
      case "$link" in http*://*|mailto:*|\#*) continue;; esac
      target="${link%%#*}"; [ -n "$target" ] || continue
      [ -e "$dir/$target" ] || [ -e "$target" ] || printf 'BROKEN: %s -> %s\n' "$f" "$link"
    done
done
```

Clean state: no `BROKEN:` lines. Known cross-references the recipe must see intact after all waves: `README.md → docs/*`; `docs/COMPONENT_DESIGN_GOALS.md → ../AGENTS.md`, `CBSE_TESTING_GUIDE.md` (no dead alpha3 file link); `docs/CBSE_TESTING_GUIDE.md → ../Makefile`, `../AGENTS.md`; `devlog/changes/README.md → ../../MANAGER.md`, `../../AGENTS.md`.

**Test tier:** `make test-fast` rc=0 (includes `verify-generated` via its dependency) recorded per slice: mandatory evidence for A2 and A3 per the repository test contract (Go-tree and build-contract changes), and required for A1 and A4 as charter §8 no-collateral proof. `make test-smoke` is **not** run by any Package-A slice (no cluster operations).

**Containment:** `git status --short` shows changes exactly inside the slice's partition — nothing outside it (the manager rejects any settlement otherwise; manager-owned devlog-index/charter-status edits and other slices' Wave-1 co-tracked changes are the only legal cohabitants, and never inside your partition).

**Forbidden shortcuts:** weakening/skipping any test or check; making the grep recipes pass by deleting evidence rather than scrubbing prose (A2/A3 only); substituting real private values for placeholders; touching blocked items; editing any spec/charter/`MANAGER.md`; committing.

## 8. Completion and handoff

Status vocabulary per slice: `complete` / `incomplete` / `verification-blocked`, recorded with evidence in [`IMPLEMENTATION_HANDOFF.md`](IMPLEMENTATION_HANDOFF.md) by the manager after each settlement, along with: the accepted delivery, test receipts (exact commands, rc), grep/link outputs, attestation lines, and any blocked sub-steps. Workers leave the worktree reviewable (no commits, P6); the user decides checkpoint commits from the manager's suggested commit plan. The post-A3 observation duty (R2): the first dispatch that starts after A3's settlement must show `make test-fast` evidence under the rewritten `AGENTS.md`; the manager records that confirmation in the handoff when it exists in this run (otherwise lists it as a standing blocker). Gate status snapshot for this run:

| Gate | State this run |
|---|---|
| D1 | **alpha2 / alpha3 confirmed** (deletions licensed, including the `COMPONENT_DESIGN_GOALS.md` fix); `test/compat/eds-sm/` + root `hack/` **pending** — sub-steps blocked |
| D3 | **relocate** — A4 stages the preserved copy; public `cluster-smoke.yml` deletion blocked on the user's preservation confirmation (follow-up dispatch) |
| D2 | informational — user creates cbse-labs when accepting A4's manifest; user-executed relocation |
| D4 / D5 / D6 | informational (B-package milestones); untouched |
