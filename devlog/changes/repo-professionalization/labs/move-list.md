# cbse-labs relocation runbook (D2, user-executed)

> **Superseded 2026-09-25:** the consolidation decision returned all relocated content to the public cbse repository and retired the private cbse-labs repository; this runbook is kept as the historical record of the D2 relocation it executed.

**Nothing in this file is executed by the slice.** R1/P6: the relocation is user-executed at D2, after the user creates the private `cbse-labs` repository and approves this manifest (charter §9, gate D2). This runbook is the executable ordering; A4 authored it and created no moves, deletions, or commits.

Inputs: the content inventory in [inventory.md](inventory.md); the target skeleton from [PROBLEM.md](../PROBLEM.md) §5; the disposition table in PROBLEM.md §6.

## 0. Precondition

- The private `cbse-labs` repository exists and is cloned locally at a path below referred to as `$LABS` (external action — user, P6).
- This public repository is at a commit containing all settled Package-A changes, with a reviewable `git status` (uncommitted Package-A work must be committed by the user before D2 so the moved history is complete).

## 1. Suggested cbse-labs directory skeleton (charter §5)

```text
cbse-labs/
├── k3s/                 # K3s dev-box manifests & feature-gate config
├── registry/            # Harbor/registry conventions (layout, naming, cleanup)
├── env/                 # env-value provider (filled copy of env-provider.template.sh)
├── ci/                  # self-hosted runner definition + relocated cluster-smoke workflow copy
├── devlog/              # devlog history (the whole devlog/** tree, step 3 below)
├── agents/              # agent docs (CODEDOCUMENTATION_AGENT.md, ORCHESTRATION_NOTES.md)
├── MANAGER.md           # manager contract
├── project-status.md    # from docs/project-status.md
└── papers/              # internal paper and handoff material
```

Skeleton notes:

- `k3s/`, `registry/`, `env/`, `papers/` have **no tracked source in this repository** — they are target locations for maintainer-owned material the user assembles (dev-box manifests and feature-gate config, registry conventions, filled environment provider, paper/handoff material).
- `ci/` receives the preserved workflow copy: the byte-identical staging at [`ci/cluster-smoke.yml`](ci/cluster-smoke.yml) in this tree becomes `cbse-labs/ci/...` or, at the user's discretion, `cbse-labs/.github/workflows/cluster-smoke.yml` — see [cluster-smoke-relocation.md](cluster-smoke-relocation.md) for the runner-definition pairing.
- `env/` starts from [env-provider.template.sh](env-provider.template.sh) (placeholders only; values are filled inside cbse-labs, never in this repository).
- **Program governance** (charter `devlog/changes/repo-professionalization/PROBLEM.md` + `prompts/`, and this package's `devlog/changes/public-repo-hygiene/` directory including these manifests) arrives inside the whole `devlog/` move of step 3.

## 2. Ordered moves (source → target)

For each row: copy the source into the `cbse-labs` working tree, commit there, then remove the source from this repository and commit here. The user performs both commits (P6).

| # | Source (this repo) | Target (`cbse-labs/`) | Commit note (both repos) |
|---|---|---|---|
| 1 | `MANAGER.md` | `MANAGER.md` | D2: relocate MANAGER.md |
| 2 | `agents/` (whole tree) | `agents/` | D2: relocate agent docs |
| 3 | `devlog/` (whole tree, incl. program governance) | `devlog/` | D2: relocate devlog history |
| 4 | `docs/project-status.md` | `project-status.md` | D2: relocate internal project status |

Copy-paste form (run from this repository's root):

```bash
# 1. MANAGER.md
cp MANAGER.md "$LABS/MANAGER.md"
git rm MANAGER.md

# 2. agents/
cp -R agents "$LABS/agents"
git rm -r agents

# 3. devlog/ (wholesale — includes this labs/ tree)
cp -R devlog "$LABS/devlog"
git rm -r devlog

# 4. docs/project-status.md
cp docs/project-status.md "$LABS/project-status.md"
git rm docs/project-status.md
```

In `cbse-labs`, after each copy: `git add <target> && git commit -m "<commit note from the table>"`. In this repository, after each removal: `git commit -m "<same note>"`.

> Step 3 copies this very `labs/` tree, so after D2 the manifests live in cbse-labs and the public repository loses them together with the rest of the development process.

## 3. What stays public

| Path | Why it stays |
|---|---|
| `README.md`, `LICENSE`, `CONTRIBUTING.md`, `CHANGELOG.md`, `SECURITY.md` | Product surface and OSS staples (A1) |
| `AGENTS.md`, `Makefile` | Public-tone test contract and build entry points (A3) |
| `experiment-operator/`, `scenario-manager/`, `component-templates/`, `go.work`, `go.work.sum` | The module trees (alpha4 product) |
| `test/` | Test harness and e2e suite (private-tier specifics are C1's work, R4 — untouched in Package A) |
| `docs/CLUSTER_REQUIREMENTS.md`, `docs/CBSE_TESTING_GUIDE.md`, `docs/COMPONENT_DESIGN_GOALS.md` | Public documentation (A3/C1) |
| `.github/workflows/verify.yml` | Public CI; kept and never weakened (P1) |

`.github/workflows/cluster-smoke.yml` is the exception handled under D3, not D2 — see [cluster-smoke-relocation.md](cluster-smoke-relocation.md) for its runbook and explicit deletion condition.

## 4. Post-move manager note (charter R1, final clause)

After D2, the manager contract, the agent docs, the devlog history, and this program's charter and prompts no longer exist in the public repository; the manager and workers work from the cbse-labs copies. Orientation for every later run: orchestration duties and the worker model are read from `cbse-labs/MANAGER.md` and `cbse-labs/agents/`; feature specifications, slice conventions, and the program charter are read from `cbse-labs/devlog/` (charter at `cbse-labs/devlog/changes/repo-professionalization/PROBLEM.md`); dispatches that touch the product surface cite the cbse-labs governance documents by their new paths. The public repository's `AGENTS.md` remains the normative test and cluster-safety contract for work that happens in the public tree — the cbse-labs governance documents do not replace it, they sit beside it as the maintainer-process contract.

## 5. Post-move verification

- In `cbse-labs`: `git status` clean; the charter is present at `devlog/changes/repo-professionalization/PROBLEM.md`; this `labs/` tree is present under the same relative path.
- In this repository: `git status` clean; the private-string scan recipes of PROBLEM.md §4/§11 no longer match moved content (the moved files left the tracked tree; what remains must still scan clean per P5).
- Run the documentation-link check (PROBLEM.md §11 item 5, recipe in the public-repo-hygiene umbrella §7) over the surviving public docs: any link from a public doc into a moved path must be re-anchored in the same D2 pass, since the targets no longer exist here.
