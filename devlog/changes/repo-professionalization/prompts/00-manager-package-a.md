# Manager program prompt — author and dispatch Package A (public-repo-hygiene)

You are the repository Manager for the CBSE repo. This run's objective is to convert the repo-professionalization charter into an authored Package-A specification set and drive it through dispatched, verified workers. Work autonomously inside this instruction; keep every result bounded, reviewable, and resumable.

## Read order and authority

Before authoring anything:

1. Read [`../PROBLEM.md`](../PROBLEM.md) completely. It is the program charter and owns all cross-cutting contracts: vision (§1–2), the eight operating principles P1–P8 (§3), the evidence base with exact strings (§4), the disposition table (§6), the package definitions (§7), sequencing rules (§8), milestone and user decision gates M0–M3 / D1–D6 (§9), the risk register R1–R6 (§10), the fresh-clone acceptance checklist (§11), and the non-goals (§13). Nothing in this prompt overrides the charter; on any conflict, the charter and the user's live decisions win.
2. Read the repository-root [`../../../../MANAGER.md`](../../../../MANAGER.md) completely — it is your operating contract: the FEATURE.md authoring contract (including when the Gang-of-Four section is legitimately marked non-applicable), the worker model policy including the pi terminal-adoption launch path and the runtime attestation requirement, the five-part Task-spec contract, the Spawn/Monitor/Orchestrate/Kill rules, and the completion accounting.
3. Read [`../../../../AGENTS.md`](../../../../AGENTS.md) — the current repository test and safety contract binding every worker. Slice A3 rewrites it mid-program (charter risk R2): workers dispatched before that wave operate under the current text; after it lands, later dispatches are checked against the rewritten text.
4. Read [`../../README.md`](../../README.md) — the development-log index you must keep current.

Author nothing for Packages B and C: their gates are closed. Write no B/C specs, dispatch no B/C work, and do not "pre-write" them (charter §12.4).

## Step 1 — batch the open user gates before authoring

Ask the user, in ONE batched message, exactly these two charter gates (§9) and stop until answered:

- **D1** — deletion confirmation, itemized: `experiment-operator/api/alpha2/`, `experiment-operator/api/alpha3/`, `test/compat/eds-sm/`, `hack/`. No A2 dispatch before every listed item is explicitly confirmed.
- **D3** — the fate of `.github/workflows/cluster-smoke.yml`: relocate (preserve a copy for the future cbse-labs repo) or keep as an interim self-hosted leg until Package C. If relocation is chosen but cbse-labs does not yet exist (D2 open), the preserved copy is staged inside the A4 deliverables and the public file is deleted only after the user confirms the preservation.

Note in the same message: D2/D4/D5/D6 remain informational for this run, and the user performs all git operations (P6) — workers leave everything uncommitted.

If the user defers any answer, author the dependent content with that sub-step explicitly marked `blocked` on the open gate and dispatch around it. Never approximate, never decide a gate yourself.

## Step 2 — author the Package-A specification set

Create `devlog/changes/public-repo-hygiene/` (register it in the devlog index with status `in-progress`) with this structure:

```text
public-repo-hygiene/
├── FEATURE.md               # umbrella: cross-cutting contracts for this package
├── IMPLEMENTATION_HANDOFF.md  # routing record, simrunner conventions (complete / incomplete /
│                             # verification-blocked; evidence tied to revisions or unchanged worktree)
├── slices/
│   ├── A1-oss-staples.md
│   ├── A2-dead-trees.md
│   ├── A3-private-infra-scrub.md
│   └── A4-cbse-labs-preparation.md
└── prompts/                 # one dispatch spec per worker you launch (Task-spec contract output)
```

**Umbrella `FEATURE.md` owns** (extracted from the charter, not improvised): mission (§1–2), scope (Package A only), a workflow-decision table derived from §6 (only the A-relevant rows), the P1–P8 principles verbatim, the gate status table (§9) with this run's D1/D3 answers recorded, the shared verification method (grep recipes from §4, scoped per slice; the R3 link-check list; `make test-fast` / `verify-generated` requirements), the read order for every worker (umbrella → own slice → `AGENTS.md`; all three are normative and read-only for workers), the wave plan with the exact file-ownership partition (Step 3 below), and the GoF design-pattern section marked non-applicable **with justification** (this package introduces no new object structure; a documentation-only package may mark the section non-applicable but may not omit it).

**Each slice spec (`slices/A*.md`) is fully self-contained and normative for its worker.** It embeds the five-part Task-spec contract — Target: the exact file/directory ownership partition; Change: the concrete result; Constraints: the applicable P-principles and repository guardrails verbatim (A3 must carry R2's atomicity rule and R3's link list; A2 carries R3 for the doc link it must fix); Ownership: the disjoint file list — anything outside is untouchable; Observable acceptance: the scoped, re-runnable recipe. Additionally per slice:

- **A1** — owns `README.md` (product rewrite), `CONTRIBUTING.md`, `CHANGELOG.md` (Keep-a-Changelog format seeded from `git log`), `SECURITY.md`, and LICENSE text *verification* (a LICENSE deviation is reported to the user, never edited).
- **A2** — owns only its D1-confirmed deletions plus the single `docs/COMPONENT_DESIGN_GOALS.md` reference fix that deletion forces; its acceptance recipe is scoped to its own deletions and must NOT be judged on private strings that A3 owns (charter §8, R4); mandatory `make test-fast` rc=0 (includes `verify-generated`) evidence; documentation-cluster smoke is not required.
- **A3** — owns `Makefile` (private default removed; `CBSE_REGISTRY` documented as required, environment-provided, never embedded private values), `AGENTS.md` (public-tone rewrite that keeps the tier contract and cluster-safety rules generic — the user-facing line sets stay truthful for the very next runs, R2), `docs/CBSE_TESTING_GUIDE.md` (re-anchored, public tone), and the D3-decided workflow-file sub-step; mandatory `make test-fast` rc=0 evidence after landing.
- **A4** — owns only new files it creates under a new `devlog/changes/repo-professionalization/labs/` subdirectory (never `PROBLEM.md`, never `prompts/`): the cbse-labs content inventory, the move-list for `MANAGER.md` / `agents/**` / `devlog/**` / `docs/project-status.md`, the env-provider script template (`CBSE_REGISTRY`, `CBSE_REGISTRY_AUTH_FILE`, `KUBECONFIG` — template only, no private values), and the relocation runbook for the user to execute at D2.

## Step 3 — dispatch as parallel-safe waves

Open one Run (`run-create`), create the four Tasks with only their real ordering dependencies, then:

- **Wave 1 — parallel, two workers:** A1 ∥ A2. Their ownership partitions are disjoint; that is precisely what licenses the parallel dispatch. Do not let either worker expand "nearby" files; a discovered gap becomes a follow-up dispatch, never an ownership handwave.
- **Wave 2 — after Wave 1 fully settles, intentionally serialized behind it (R2):** A3 ∥ A4. A3 rewrites the contract under which all workers work; A4 is pure new-file creation beneath the charter directory, so the two are ownership-disjoint.
- After A3 lands, confirm at least one subsequent dispatch's normal `make test-fast` evidence shows workers still satisfy the rewritten `AGENTS.md`; record that confirmation in the handoff.

Launch every pi worker per the Worker model policy — no `--model` flag on `worker-start`: `terminal create --worktree current --command "pi --model ai.forge/qwen3.8-27b-nvfp4"`, `terminal wait --for tui-idle`, then `worker-start --task <task_id> --terminal <handle> --worktree current`. Every dispatch spec (the `prompts/<slice>.md` you write) must include: the read order, the read-only treatment of all specs, `no commits` (P6), and the runtime attestation requirement — the worker runs `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` at its first checkpoint and repeats the verbatim output in its `worker_done` executive summary.

## Step 4 — settle and verify (you are the evidence reviewer, not the implementer)

Run the standard supervised loop: `check --wait` on `worker_done,escalation,question`, FIFO order, every message processed before its ack, questions escalated to the user, never answered by typing into a worker's terminal. Accept a settlement only when **all** hold:

1. the attestation line reads `ai.forge/qwen3.8-27b-nvfp4`;
2. the slice's scoped acceptance recipe passes **and you re-ran it yourself** from a fresh shell (grep recipe, R3 link check; for A2/A3 additionally the recorded `make test-fast` rc=0);
3. `git status --short` shows changes exactly inside the slice's ownership partition — nothing outside it;
4. the handoff records the slice state with its evidence.

Reject prose-only claims. Failures and stalls follow the MANAGER.md Kill/Retry rules: positive proof only, `--retry-of` with explicit placement, and the three-consecutive-failures circuit-breaker is reported to the user, never routed around. Settle every terminal — release after acceptance, no `reclaimable` rows when your turn ends.

Maintain, as your own charter-keeping duties: `IMPLEMENTATION_HANDOFF.md` after every settlement; the two devlog-index rows (charter row + package row); and the `> **Status:**` line of `PROBLEM.md` reflecting the current package state. Make no other edits to the charter — its content is user-owned.

## Step 5 — final report to the user

End with the MANAGER.md per-Task report (Outcome / Evidence / Terminals / Blockers), plus:

1. **Gates** — D1/D3 answers received; every gate still pending listed with what it blocks.
2. **Deliverables** — the paths: umbrella `FEATURE.md`, four slice specs, the dispatch prompts, and the handoff.
3. **Fresh-clone checklist (§11)** — item by item: passes now (with evidence), improved but partial, blocked for later packages (expect item 2's grep-clean state to pass only after Wave 2).
4. **Commit plan** — a suggested logical commit breakdown for the user to execute (example: one commit per slice, ordered staples-first, deletions isolated in a clearly labeled commit).
5. **Next user actions** — commit decision; review of A4's labs prep; D2 (create cbse-labs) whenever the user accepts the prepared content; confirmations for any preserved-copy deletion in A3/D3; B/C gates unchanged and untouched.

## Hard prohibitions

- Dispatch nothing before Step 1's gates are answered; approximate nothing the user deferred.
- Delete nothing outside the D1-confirmed list; touch nothing from charter §13; keep B/C gates closed.
- No commits, no pushes, no repository or registry or external actions (P6: user-owned).
- No edits to `MANAGER.md`; no edits to charter content beyond the Status line; no edits outside the Package-A directory except the devlog index rows.
- Never accept an attestation mismatch or unverifiable evidence; never leave a settled terminal without a next-owner decision.
