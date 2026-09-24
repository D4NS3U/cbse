# cbse-labs content inventory

**Manifest only — no relocations were executed by this slice.** The user performs the moves at D2 (charter §9); P6/R1 bind. This slice (A4 of [`public-repo-hygiene`](../../public-repo-hygiene/FEATURE.md)) only lists the content destined for the private `cbse-labs` repository; the executable, ordered steps live in [move-list.md](move-list.md).

Scope: every tracked item the program charter ([`PROBLEM.md`](../PROBLEM.md), §2/§5/§6) classifies as the maintainers' world — development-process documentation, internal status, and private-infrastructure content — that must not remain in the public product repository.

## Items destined for cbse-labs

| Source path (this repo) | Proposed `cbse-labs/` target | Rationale (one line) |
|---|---|---|
| `MANAGER.md` | `MANAGER.md` | The manager/orchestration contract (dispatch rules, internal worker-model policy) — development process, not product surface. |
| `agents/CODEDOCUMENTATION_AGENT.md` | `agents/CODEDOCUMENTATION_AGENT.md` | Role prompt for the parallel documentation agent — internal development tooling. |
| `agents/ORCHESTRATION_NOTES.md` | `agents/ORCHESTRATION_NOTES.md` | Orchestration notes for dispatched agent workers — internal development tooling. |
| `devlog/` (the whole tree, `devlog/**`) | `devlog/` | The full development-log history: feature specifications, slice prompts, implementation handoffs, and the index. **The program governance moves wholesale with the rest** — `devlog/changes/repo-professionalization/` (the charter `PROBLEM.md`, the program prompt, and this `labs/` preparation tree) and `devlog/changes/public-repo-hygiene/` (this package's umbrella, slices, prompts, handoff — including these manifests) are process history, not product content. |
| `docs/project-status.md` | `project-status.md` | Internal roadmap/product-boundary status; an internal document, not part of the public documentation set. |

## Notes

- **`artifacts/`** — local, git-ignored output (`artifacts/test/<run-id>/` test diagnostics, `artifacts/doc/`, `artifacts/test-images/`). Nothing under `artifacts/` is tracked, so D2 does not relocate it as a git move; local retention (or cleanup) is the user's call, not a repository operation. The charter's disposition line for `artifacts/doc/**` (PROBLEM.md §6) likewise names untracked local content.
- **`.github/workflows/cluster-smoke.yml`** — handled under D3, not D2: the byte-identical preserved copy is staged at [`ci/cluster-smoke.yml`](ci/cluster-smoke.yml); [cluster-smoke-relocation.md](cluster-smoke-relocation.md) carries the runbook and the explicit deletion condition.
- **Hygiene (P5):** this inventory names items and paths only. It contains no private operational values — no endpoints, registry names, host paths, or runner labels as literals — so it stays scan-clean by design.
