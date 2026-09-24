# Repo professionalization — problem description and program charter

> **Audience:** the repository Manager (the Orca coordinator role defined in the repository-root [`MANAGER.md`](../../../MANAGER.md)).
> **Purpose:** this document is the intake and problem description for a multi-milestone program. The Manager reads it, asks the user the open decision questions, then authors the required `FEATURE.md` specifications (one per package, under `devlog/changes/<package>/FEATURE.md`) and dispatches worker agents on them per the `MANAGER.md` worker model policy.
> **Status:** Package A complete — A1–A4 plus all follow-ups settled and reviewer-verified 2026-09-24 (header pass, SECURITY/D4NS3U, LICENSE appendix; user enabled GitHub private vulnerability reporting). Gate update: **D4 resolved — the repository was already public** at `github.com/D4NS3U/cbse`; the public surface still shows the pre-professionalization state (tip `9f01e82`) until the user lands the uncommitted P6 change set (204 files, +2688/−1847). Next: user commit plan, D2 (cbse-labs per `labs/move-list.md`), M0 external-reviewer fresh-clone dry-run. Package B remains gate-locked (needs M1 stability + D5 ghcr naming in addition to D4); Package C unchanged.
> **The Manager may not implement anything itself** outside its `MANAGER.md` duties (spec authoring, dispatch, verification, settlement). All file changes belong to workers; all git operations and all actions outside this repository belong to the user.

## 1. Vision

CBSE is a Kubernetes-native framework for executing simulation experiments. The end state is a **portable, widely usable application that simulation experts can operate for large-scale simulation workloads** on their own clusters, without knowing anything about this repository's development infrastructure.

The consumer path is:

```
simulation expert ──► README quickstart ──► helm install (chart) ──► apply SimulationExperiment ──► results
                      docs/CLUSTER_REQUIREMENTS (their cluster admin: K8s >= 1.30, UserNamespacesSupport)
                      component-templates/* + COMPONENT_DESIGN_GOALS (their custom EDS/Translator/PostProc images)
                      public registry images, digest-pinned
```

For that audience, the repository is the **product surface**: readable, installable, verifiable, and free of private development infrastructure. The framework itself (Experiment Operator, Scenario Manager, component templates, future Helm chart) is close to feature-stable; the repository is not yet in a state that the open-source community would judge professional.

## 2. Problem statement

The repository is **dev-heavy**: it mirrors the unfinished, institution-internal development process rather than a product. Concretely (all verified in the working tree):

1. **Private platform baked into tracked files.** Anyone cloning the repo sees infrastructure that works only on the maintainers' machines:
   - `Makefile`: `CBSE_REGISTRY ?= registry.unibw.de/i31bdase/cbse-test` as a default value.
   - `AGENTS.md`: smoke cluster endpoint `https://192.168.101.245:6443` and the Linux-agent kubeconfig path `/home/d4ns3u/.kube/config`.
   - `.github/workflows/cluster-smoke.yml`: `runs-on: [self-hosted, linux, x64, cbse-k3s]` — a private self-hosted runner against the private K3s dev box.
   - Shared-cluster contention choreography in `test/harness/` (`acquire-lock.sh`, lease handling, `cbse-e2e-<run-id>` namespace retention flags, `registry-cleanup.sh` binding to the private Harbor).
2. **Internal process artifacts at the product surface.** `devlog/` (feature specs, slice prompts, implementation handoffs with internal evidence strings), `MANAGER.md` (Orca coordinator contract with the internal `ai.forge` model policy), `agents/` (worker role prompts and orchestration notes), `docs/project-status.md` (internal roadmap), and a git-ignored `artifacts/` output directory all sit beside the code.
3. **Retired code still present.** `experiment-operator/api/alpha2/` and `api/alpha3/` remain on disk although alpha2/alpha3 are fully retired and unreconciled; `test/compat/eds-sm/` documents a manual alpha-era test flow; an empty `hack/` directory exists.
4. **Open-source staples missing.** No `CONTRIBUTING.md`, no `CHANGELOG.md`, no `SECURITY.md`; the README is oriented to the current repository, not to installation and use; the only tag is the paper-linked `v0.1-jos-paper`; images are published only to the private Harbor registry; no release process exists.
5. **The test tiers are professional content, not clutter — the private parts of them are not.** `make test-fast` (module tests, race suites, envtest, CRD `verify-generated`) already runs on public `ubuntu-latest` CI (`verify.yml`) and must never be weakened or removed. What is unprofessional is only: the private cluster tier's requirement for the K3s dev box, the private registry default, and the self-hosted runner leg. Removing tests because the platform is private would damage the product; removing the private platform while re-pointing the tests to public, ephemeral infrastructure is the actual goal (see [Milestone gates](#9-milestone-gates-and-user-decision-gates), Package B/C).

The community-facing acceptance standard is plain: **a fresh clone on stock hardware must build, test, and explain itself with zero private knowledge** — and an outside reviewer should find no reference to machines, registries, credentials, or internal process artifacts of the developing institution (attribution like the LICENSE and copyright headers is explicitly exempt).

## 3. Operating principles (invariants for every slice the Manager authors)

- **P1 — Tests survive publicly or not at all.** No test, tier, or assertion is deleted before its public replacement is green. `test-fast` and `verify-generated` are never removed or made private-only.
- **P2 — Private platform out, not tests out.** The K3s dev box, Harbor conventions, self-hosted runner, and lock/lease choreography leave the public repository; their capability is transferred (public ephemeral clusters, public registry, release validation) before the private instances are dropped.
- **P3 — Attribution stays.** Institution name in LICENSE, copyright headers, and paper references is legitimate attribution and must survive the scrub. Operational infrastructure (IPs, registry names, runner labels, kubeconfig paths, hostnames) must not. A slice spec must state this distinction verbatim before scrubbing.
- **P4 — Working contracts stay self-consistent.** `AGENTS.md` is normative for every worker while it works; the slice that rewrites it must land it in a state that is simultaneously true for subsequent runs, and the update is recorded in the slice handoff. `MANAGER.md` is the Manager's own contract: the Manager must not dispatch a slice that deletes it (see R1).
- **P5 — No secrets exposure.** Before any publicization gate, a dedicated audit slice must prove by recipe (grep patterns + review) that no tracked file contains credentials, tokens, private kubeconfigs, or registry auth material. Known good state: registry auth arrives via `CBSE_REGISTRY_AUTH_FILE` externally; harness files must not grow private values during M0.
- **P6 — User owns git and all external actions.** Workers leave the worktree reviewable and create no commits. The user performs all commits, tags, pushes, repository creations, and registry/publication actions, advised by Manager reports. Every external-action requirement becomes an explicit user decision gate in the Manager's report.
- **P7 — Product-first surface.** Everything the simulation expert needs (README quickstart, install path, extension templates, cluster requirements, docs) is first-class; everything else is moved out, deleted, or documented for contributors in `CONTRIBUTING.md`.
- **P8 — No re-architecture during tidying.** This program changes packaging, CI, docs, and placement. It does not change runtime behavior, module boundaries, the monorepo topology (submodules were reviewed and rejected: `git` submodules do not serve Helm and the monorepo protects CRD single-sourcing and coherent image-set versioning), or the alpha4 contracts.

## 4. Current-state evidence base (for slice verification recipes)

The Manager's specs must turn this inventory into `grep`-able observable acceptance criteria. Exact strings and paths, verified in the working tree at charter time:

| Class | Artifact | Location |
|---|---|---|
| Private default | `registry.unibw.de/i31bdase/cbse-test` | `Makefile` (`CBSE_REGISTRY ?=`) |
| Private endpoint | `https://192.168.101.245:6443` | `AGENTS.md` |
| Private host path | `/home/d4ns3u/.kube/config` | `AGENTS.md` |
| Private runner | `runs-on: [self-hosted, linux, x64, cbse-k3s]` | `.github/workflows/cluster-smoke.yml` |
| Shared-cluster glue | `test/harness/acquire-lock.sh`, `registry-cleanup.sh`, lease/retention flags in `smoke.sh`/`clean.sh` | `test/harness/` |
| Internal process docs | `docs/project-status.md`, `devlog/**` | tracked |
| Agent process docs | `MANAGER.md`, `agents/CODEDOCUMENTATION_AGENT.md`, `agents/ORCHESTRATION_NOTES.md` | root, `agents/` |
| Retired code | `experiment-operator/api/alpha2/`, `experiment-operator/api/alpha3/` (note: `docs/COMPONENT_DESIGN_GOALS.md` still links to `api/alpha3/simulationexperiment_types.go`) | tracked |
| Alpha-era test docs | `test/compat/eds-sm/` | tracked |
| Empty dir | `hack/` | untracked content, directory present |
| Release state | single tag `v0.1-jos-paper`; no CHANGELOG; no CONTRIBUTING/SECURITY; images private-only | `git tag`; root listing |
| Public CI (good) | `verify.yml` on `ubuntu-latest` runs `make test-fast` | `.github/workflows/verify.yml` |
| Product docs (good, to keep) | `docs/CLUSTER_REQUIREMENTS.md` (self-declared prerequisite source for the future chart, incl. "Summary for a future Helm chart"), `docs/CBSE_TESTING_GUIDE.md`, `docs/COMPONENT_DESIGN_GOALS.md` (needs a content refresh — still alpha3-era framing), module READMEs | `docs/`, modules |
| Test tiers (good, to keep) | `make test-fast` = `verify-generated` + harness self-tests + gofmt/vet + compile gates + race tests + operator envtest; `make test-smoke` = private cluster tier; `make publish-test-images` = digest-locked image build (`test/e2e/images.lock.env`) | `Makefile`, `test/` |
| Module wires (good, to keep) | `go.work` pins `k8s.io/*` v0.33.0 for the translator's buildkit graph; SM/e2e require `github.com/D4NS3U/cbse/experiment-operator v0.0.0` via relative replace | `go.work`, go.mod files |

## 5. Target end-state

```
cbse/                                  (public product repository)
├── README.md            # product-oriented: what/why, quickstart (chart-era: helm install), architecture, CI badge
├── LICENSE, CHANGELOG.md, CONTRIBUTING.md, SECURITY.md
├── AGENTS.md            # public-tone: module test contract + generic cluster safety; no private endpoints
├── experiment-operator/ scenario-manager/           # modules with their Go tests, envtest, generated CRD
├── component-templates/{translator,scenario-detail-database,...}   # the extension API for external experts (product!)
├── charts/cbse/         # (M2) the install path; CRD synced from verify-generated; templates seeded from proven stack.yaml
├── test/                # module-adjacent unit suites + public e2e/chart validation; harness reduced to public-runnable scripts
├── docs/                # CLUSTER_REQUIREMENTS, TESTING_GUIDE, COMPONENT_DESIGN_GOALS (refreshed), release guide
└── .github/workflows/   # verify.yml (public, hosted); public cluster-validation; no self-hosted leg

cbse-labs/  (private repository, user-created — D2)   # the maintainers' world, not the community's:
├── k3s dev-box manifests & feature-gate config, Harbor/registry conventions, env-value provider (CBSE_REGISTRY, ...),
│   self-hosted runner definition + relocated cluster-smoke workflow copy
├── devlog/ history, MANAGER.md, agents/ docs, program governance for this charter, project-status
└── internal paper and handoff material
```

## 6. Disposition table (normative for all slices)

| Current item | Fate | Gate |
|---|---|---|
| Module tests, envtest, race suites, `verify-generated`, `test-fast` | Keep, mandatory, public forever | — |
| `test/e2e` Ginkgo suite | Keep; re-target to chart-installed systems in M2 | B3 |
| `test/harness/build-images.sh`, `image-lock.sh` | Keep, genericized → public image pipeline (ghcr.io) | B1 |
| `test/harness/{smoke,preflight,clean,diagnose}.sh` private-cluster specifics | Extract generic checks; private specifics move to cbse-labs or die at M3 | A3, C1 |
| `test/harness/{acquire-lock.sh, registry-cleanup.sh, install-kubectl.sh}` + lock/lease/retention flags | Move to cbse-labs or delete (they solve shared-private-cluster contention) | D1, C1 |
| `Makefile` private registry default | Default removed; `CBSE_REGISTRY` becomes required-but-unset, supplied by the user's environment/cbse-labs provider | A3 |
| `AGENTS.md` private endpoint/path lines; overall tone | Rewrite public-tone, keep the tier contract and cluster-safety rules generic | A3 |
| `.github/workflows/cluster-smoke.yml` (self-hosted leg) | Relocate to cbse-labs; public repo keeps `verify.yml` until B2/B3 public cluster validation exists | D3 |
| `MANAGER.md`, `agents/**`, `devlog/**`, `docs/project-status.md`, `artifacts/doc/**` | Move to cbse-labs (user-executed), after Manager prepares the target content | D2, R1 |
| `experiment-operator/api/alpha2/`, `api/alpha3/` | Delete (retired, unreconciled); fix the `COMPONENT_DESIGN_GOALS.md` reference that still points into `api/alpha3` | D1 |
| `test/compat/eds-sm/` | Delete (alpha-era manual flow) | D1 |
| Empty `hack/` | Delete | D1 |
| `docs/CLUSTER_REQUIREMENTS.md`, `CBSE_TESTING_GUIDE.md` | Keep; public tone; TESTING_GUIDE re-anchors tiers when C1 rewrites them | A3, C1 |
| `docs/COMPONENT_DESIGN_GOALS.md` | Keep; content refresh to alpha4/product framing (requires code-literate worker) | B?—see note |
| README.md | Rewrite: product what/why, architecture, quickstart (source-install now; chart-install forward reference), badge, docs links, license | A1 |
| LICENSE | Verify explicit Apache-2.0 text and consistent copyright headers | A1/P3 |
| Missing: `CONTRIBUTING.md`, `CHANGELOG.md`, `SECURITY.md` | Add (content sketched in Package A) | A1 |

Note on `COMPONENT_DESIGN_GOALS.md`: its refresh to alpha4-era accuracy is real editorial work, not scrubbing — schedule it as its own documentation slice once the A-packages settle; the chart-era (`COMPONENT_DESIGN_GOALS` doubles as the extension guide the Chart consumer reads). `MANAGER.md`'s GoF discipline applies to any production of design docs only where code structure changes; documentation slices mark the section non-applicable with justification instead of omitting it.

## 7. Program packages (what the Manager turns into FEATURE.mds)

The Manager authors **one `FEATURE.md` per package** at the moment its gate opens (not before, so specs never go stale), under `devlog/changes/<package>/FEATURE.md`, using its standard authoring contract (mission, scope, workflow decisions, GoF section where applicable, guardrails, slices with the five-part task contract, verification gates, completion vocabulary). Suggested packages:

**Package A — `public-repo-hygiene`** (dispatchable now; gate: none)
- A1 OSS staples: `CONTRIBUTING.md` (module-tier participation rules, dev setup, cluster-safety generic, no private infrastructure assumptions), `CHANGELOG.md` (Keep-a-Changelog format seeded from `git log` history), `SECURITY.md` (private disclosure contact, supported-versions pointer), README product rewrite, LICENSE verification.
- A2 Dead trees: delete `api/alpha2/`, `api/alpha3/`, `test/compat/eds-sm/`, `hack/`; regenerate/verify nothing else — `verify-generated` + `test-fast` must stay green; fix the `COMPONENT_DESIGN_GOALS.md` alpha3 link appropriately at deletion time (point at the retired-API notice instead of the deleted file).
- A3 Private-infrastructure scrub: `Makefile` default removal (env-required with documented variables), `AGENTS.md` public rewrite (P3/P4 constraints verbatim in the spec), `docs/CBSE_TESTING_GUIDE.md` tone/anchor updates, CI leg separation (delete the private workflow file from the public repo only after D3 decides relocate-vs-delete).
- A4 cbse-labs preparation (documentation-only, in-repo): content inventory, move-list, env-provider script template (`CBSE_REGISTRY`, `CBSE_REGISTRY_AUTH_FILE`, `KUBECONFIG`), runbook for relocating `cluster-smoke.yml`, MANAGER/agents/devlog handover guidance. The actual relocation is user-executed (D2).

**Package B — `public-release-pipeline`** (gate: M1 stable framework + D4 repo public + D5 ghcr naming)
- B1 Genericized image build/publish workflow to the public registry (reuse `build-images.sh`/`image-lock.sh`; digest-locked; semver-tagged pushes).
- B2 Public ephemeral-cluster spike: kind (or equivalent) on GitHub-hosted runners with the `UserNamespacesSupport` feature-gate requirement for the Translator's rootless BuildKit chain — the single known engineering risk. Deliverable: a green public e2e run, or a documented fallback split (public tier covers everything except the translator image-build chain; that slice stays private-leg until runners support it) to be revisited at C1 gate.
- B3 `charts/cbse/`: CRD sync extension of `verify-generated` into `charts/cbse/crds/`; templates seeded from `test/e2e/manifests/base/stack.yaml`; values for core components and optional example components; chart schema; a chart-validation mode driven by the e2e suite against chart installs.
- B4 Release engineering: semver cutting, tag → images → chart version → GitHub Release with digest manifest; `CHANGELOG.md` maintenance duty lands here formally.

**Package C — `dev-box-sunset`** (gate: B3 chart validation green)
- C1 Remove the remaining private-tier teachings from the public repo: private-specific harness scripts and flags, self-hosted references, `AGENTS.md` tier rewrite (module tier mandatory; product tier = chart validation on releases), `CBSE_TESTING_GUIDE.md` re-anchored, harness self-test scope reduced to the public-runnable set.
- C2 Private box decommission runbook (user-executed; cbse-labs decides the K3s box's fate; not this repo's concern beyond the runbook).

## 8. Cross-package sequencing rules

- A1–A4 may run in parallel after the Manager reads the open gates; A2 and A3 both touch verified behavior — schedule A2's deletions and A3's AGENTS.md rewrite as mutually exclusive worker waves or serialize them.
- Each A-slice's observable acceptance includes: the grep recipes of §4 (for A3), `make test-fast` rc=0, and the documentation-link check (dead relative links across `README`, `docs/`, `devlog/changes/README.md`) — the Manager runs the checks itself as evidence reviewer before accepting `worker_done`.
- B and C specs are authored only when their gates open; the Manager must poll gate state at each user interaction, not assume it.
- Workers on this program are documentation/repo-tidy capable, code-literate for A2; every task spec carries the model attestation requirement per the Worker model policy (`ai.forge/qwen3.8-27b-nvfp4`) and the collective-safety rules until C1 rewrites them.

## 9. Milestone gates and user decision gates

**Milestones** (trigger-based):
- **M0 — Hygiene done**: all A-slices settled; grep recipes pass; fresh-clone passes `make test-fast` with only public prerequisites (Go toolchain, network); an external-reviewer dry-run passes (a reviewer worker executing the fresh-clone checklist below) — this is the pre-condition for D2/D3 execution by the user.
- **M1 — Framework stable**: the development roadmap states it (confidence evaluation, Finished/Failed experiment phases, PostProcessingService integration are the known remaining features). No repo work is gated on M1 until D4/D5 are also resolved.
- **M2 — Public production surface**: B-slices green; a `helm install` from the public chart with the released public digests runs the full reference chain on an empty conformant cluster.
- **M3 — Dev box sunset**: C-slices settled; the public repo no longer references any private platform component.

**User decision gates** (the Manager must ask, never decide):
- **D1** Delete confirmations for `api/alpha2`, `api/alpha3`, `test/compat/eds-sm/`, `hack/`, and shared-cluster scripts at M3 (per-item confirmation is fine — must confirm before any worker deletes retired code/history).
- **D2** Create the private `cbse-labs` repository (user action: new private repo, external to this repository) and approve the prepared relocation manifest A4 produced for `MANAGER.md`, `agents/**`, `devlog/**`, `docs/project-status.md`, and program governance.
- **D3** Decide the fate of `.github/workflows/cluster-smoke.yml` before the private leg disappears from the public workflow surface (relocate to cbse-labs now vs. keep interim self-hosted).
- **D4** Confirm the GitHub repository is (or when it becomes) public; ghcr publication and `pkg.go.dev` visibility depend on this.
- **D5** Fix the public image namespace under ghcr (e.g. `ghcr.io/d4ns3u/...`) and the chart publication channel (ghcr OCI charts vs. GitHub Pages helm repo).
- **D6** Adopt semver/release cadence and whether a Code-of-Conduct file is added.

## 10. Risk register (Manager must design slices so these cannot fire)

- **R1 — Self-referential contract loss.** `MANAGER.md` and this charter move to cbse-labs at D2; while any part of the program runs, those documents remain in this repository unmodified. The Manager must not dispatch a slice that deletes/moves `MANAGER.md` or `devlog/` (including this charter) — relocation is user-executed (P6), after which the Manager works from the cbse-labs copies per its own operating instructions.
- **R2 — Contract drift during rewrite.** Rewriting `AGENTS.md` while it governs active workers: the rewrite slice is a single atomic wave; its handoff states the new contract; immediately following worker runs must be observed to still satisfy it (their `test-fast` evidence proves it).
- **R3 — Link breakage on doc moves.** Known cross-references: `README.md → docs/*`, `docs/COMPONENT_DESIGN_GOALS.md → ../AGENTS.md, CBSE_TESTING_GUIDE.md, project-status.md, alpha3 api file`, `docs/CBSE_TESTING_GUIDE.md → ../Makefile, ../AGENTS.md`, `devlog/changes/README.md → ../../MANAGER.md, ../../AGENTS.md`. Every move/rewrite slice includes an explicit link-check task in its acceptance recipe.
- **R4 — Harness self-tests as false blocker.** `test-harness.sh` self-tests inside `test-fast` currently exercise private-cluster scripts; A3 must not remove scripts (they die at C1) — only config/docs/defaults. The sequence keeps `test-fast` green throughout M0/M1.
- **R5 — Public runner gap for UserNamespacesSupport.** B2 is the probe; if it fails, the fallback split (public tier minus translator image-build chain) is accepted at C1 gate with D-confirmation, and the limitation is published in `CLUSTER_REQUIREMENTS.md` (the install path has the same runtime prerequisite anyway, so the gap is honestly documented, not hidden).
- **R6 — Submodule re-litigation.** This program does not split the repo into submodules; if a future request reopens that, it is a new charter (the standing analysis: submodules serve neither the Helm chart nor the test tiers; the monorepo protects CRD single-sourcing and coherent image-set versioning).

## 11. Acceptance: the fresh-clone checklist (program-level definition of done)

Executed by a review worker on a fresh clone in a default environment (recommended gate: CI green locally with only public Go toolchain requirements):

1. `git clone` succeeds; `make test-fast` green with only public prerequisites.
2. Grep recipes clean: no `192.168.`, `registry.unibw.de`, `i31bdase`, `cbse-k3s`, self-hosted runner references, `/home/...kubeconfig`, or `ai.forge` in tracked files; LICENSE/copyright attribution remains (P3 distinction).
3. A contributor can follow `CONTRIBUTING.md` alone to set up and run the module tier; no internal knowledge is needed.
4. A simulation expert reading only the README, `docs/`, and `component-templates/` can state: what CBSE is, how it is installed, what their own EDS/Translator must fulfil, and what their cluster admin must enable.
5. All markdown links resolve; `CHANGELOG.md` exists with a seeded history; every settled slice's handoff names its evidence.

## 12. Manager execution instructions

1. Read this charter fully, then the repo-root `MANAGER.md` (authoring contract, worker model policy with the pi launch path and runtime attestation, Task-spec contract, settlement rules).
2. Ask the user the open gates in order: D1 (deletion list) is required before any A2 dispatch; D3 before A3's workflow-file step; D2/D4/D5 are informational until their milestones.
3. Author `devlog/changes/public-repo-hygiene/FEATURE.md` (Package A) with slices A1–A4 as ordered, self-contained task specs carrying the §4 evidence, §6 dispositions, §10 risk mitigations, and §11 acceptance criteria per slice.
4. Package-B and -C specs are written when their gates open; do not pre-write them.
5. Dispatch under the Worker model policy; every pi task spec carries the model attestation requirement; workers create no commits (P6).
6. Update `devlog/changes/README.md` with a row per new package directory (`public-repo-hygiene` now; later packages on gate-open).
7. Settle every Dispatch per `MANAGER.md` completion accounting; the Manager is the evidence reviewer for grep/link/checklist acceptance, not the implementer.
8. Report program status per milestone gate against this charter's §9; unopposed deviations require a user decision before re-steering.

## 13. Out of scope (non-goals of this program)

- Framework feature development (PostProc integration, experiment confidence evaluation, `Finished`/`Failed` phases) — that is the product roadmap, unaffected and still harness-protected per P1.
- Any behavior change in operator/SM/translator; any API change; any runtime contract change.
- Repo splitting into submodules (R6), Helm-chart-vs-monorepo re-architecture, or moving feature specs out of `devlog/` structure before D2.
- Deleting or weakening any test or tier before its public replacement is green (P1).
