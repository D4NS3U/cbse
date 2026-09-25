# Dispatch prompt — S2: Security channel resolution + public org fill-in

**Task title:** S2 — SECURITY.md disclosure channel (GitHub built-in private vulnerability reporting) + README `<your-org>` → `D4NS3U` (user decisions 2026-09-24)

**Read order (mandatory, all normative and read-only for you):** 1) `devlog/changes/public-repo-hygiene/FEATURE.md` §5 global constraints and §7 recipes; 2) repository-root `AGENTS.md` (current, rewritten public-tone text — normative for your run); 3) the two target files. On any contradiction: stop and ask via your ask channel.

**User decisions you implement (recorded 2026-09-24):** the disclosure contact placeholder in `SECURITY.md` resolves to **GitHub's built-in private vulnerability reporting** (the "Report a vulnerability" entry point on the repository's Security tab), and the public GitHub home of the repository **stays at the `D4NS3U` personal organization** (as already declared by `go.mod` and `experiment-operator/PROJECT`: `github.com/D4NS3U/cbse`).

## Target (your ownership, disjoint from the parallel L1 worker)

Exactly two files: `README.md` and `SECURITY.md`. Untouchable: everything else — in particular `LICENSE` (L1 also stays off it), `CONTRIBUTING.md`, `CHANGELOG.md`, all `docs/**`, all specs.

## Change

1. **`README.md` — resolve the four `<your-org>` placeholder lines (pre-verified locations):**
   - Line 4 (the badge caption sentence introducing the placeholder) — reword so it states the badge target truthfully (e.g., that the badge reflects the repository's public CI workflow) and no longer instructs a replacement.
   - Line 9: `https://github.com/<your-org>/cbse/actions/workflows/verify.yml/badge.svg` → `https://github.com/D4NS3U/cbse/actions/workflows/verify.yml/badge.svg`.
   - Lines 78–79 (the clone block comment and command) — resolve `<your-org>` → `D4NS3U`; the clone URL becomes `https://github.com/D4NS3U/cbse.git`.
   Keep all surrounding content byte-identical; nothing else in the README changes.
2. **`SECURITY.md` — replace the placeholder disclosure contact.** The disclosure section names GitHub's built-in private vulnerability reporting as the channel: report via the repository's **Security tab → "Report a vulnerability"** (GitHub private vulnerability reporting; the maintainers see and respond to reports privately on GitHub). Remove the `[PLACEHOLDER — …]` marker entirely. Keep the remaining structure (supported-versions statement, reporting scope, cluster-tier note) intact; adjust wording only where the placeholder's removal requires it.

**User-side companion action (NOT yours; the manager reports it):** enabling GitHub private vulnerability reporting is a repository-settings toggle the user performs in the GitHub UI (Settings → Code security and analysis → Private vulnerability reporting). You change no settings, touch no external systems, and do not state that the feature is already enabled — the text may say reports are accepted *via* that channel.

## Constraints

- Documentation-only change: the tier contract (`AGENTS.md`, current text) requires no test tier; do not run cluster operations; no commits (P6 — everything stays in the working tree for user review).
- No fabricated emails or URLs beyond the resolved GitHub paths named above; keep the institution-neutral public tone (P3/P5 discipline from umbrella §5).
- Scope discipline: exactly the two named files.

## Observable acceptance (all echoed in your worker_done; the manager re-runs each)

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat in your executive summary; mismatch → stop, change nothing, `--outcome failed`).
2. Resolution proof: `grep -rn "<your-org>" README.md SECURITY.md` → rc=1 (no placeholders remain); `grep -n "D4NS3U" README.md` shows the badge and clone URLs resolved; `grep -n "PLACEHOLDER" SECURITY.md` → rc=1.
3. Channel proof: `grep -n -i "report a vulnerability\|private vulnerability" SECURITY.md` shows the channel wording; the file contains no fabricated email/address.
4. Containment: `git status --short` shows your edits as modifications of exactly `README.md` and `SECURITY.md` (plus the parallel L1 worker's expected in-flight header edits across source files and its CDG section — recognized co-residents, outside your two files but never yours to touch or revert).
5. Diff discipline: `git diff README.md` and `git diff SECURITY.md` show only the changes described above; echo both diffs' final stats lines.
6. R3 link check (umbrella §7 recipe over the full tree) → no BROKEN lines.
