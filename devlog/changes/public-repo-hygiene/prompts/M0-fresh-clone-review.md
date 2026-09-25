# Dispatch prompt — M0: external-reviewer fresh-clone dry-run

**Task title:** M0 — External-reviewer fresh-clone dry-run of the public CBSE repository

**Your role:** You are an independent external reviewer. You have no prior context about this repository's development process. Execute the acceptance checklist below exactly as written, on a fresh clone, using only what the repository itself provides, and report the empirical truth per item. You are a reviewer, not an implementer: you fix nothing, you edit nothing, you record findings.

## Read order

Clone first, then let the repository's own documents guide you (README → docs/ → CONTRIBUTING.md → module trees). There is no other authority for you; where the repository says something inconsistent, record it as a finding rather than resolving it by opinion.

## Target (your ownership)

A scratch directory you create: `/tmp/cbse-m0-review` (fresh; delete any pre-existing dir of that name first). Inside it: the fresh clone and your review report `M0-REVIEW.md`. The public repository itself (`github.com/D4NS3U/cbse`) and any working copy of it you may find elsewhere on this machine are untouchable: no modifications, no commits, no pushes — you work only from your fresh clone and only inside the scratch dir.

## Procedure

```bash
mkdir -p /tmp/cbse-m0-review
cd /tmp/cbse-m0-review
git clone https://github.com/D4NS3U/cbse.git cbse
cd cbse
```

Then execute these five checklist items, in order. For each item, capture the exact commands you ran and their relevant output tails (not silent summaries) into scratch `cbse/M0-REVIEW.md` (write the report file inside the clone dir at the clone's top level — you may create this one file; it is untracked output, never committed).

### Item 1 — clone, build, test on public prerequisites

Run `make test-fast` in the fresh clone (this is a cold run: module downloads, codegen tooling, and envtest control-plane binaries are fetched from public sources; it may take several minutes). Record: the exact rc, the final `ok`-lines tail, and the total wall time. Only publicly obtainable tooling may be needed (Go toolchain, network, Python for the embedded runner conformance suite). If the suite fails for a missing host tool, record the missing tool and the failure verbatim; do not install system-wide packages other than with the repository's own documented mechanisms, and only if CONTRIBUTING.md or the failure itself tells you what is missing.

### Item 2 — private-infrastructure grep audit

Over the **tracked files** of the clone (`git grep`, not raw filesystem scans):

```bash
git grep -nE '192\.168\.|registry\.unibw\.de|i31bdase|cbse-k3s|/home/[^ )`]*\.kube|ai\.forge|self-hosted'
```

Record every hit. Legitimate attribution is exempt by policy: LICENSE text, copyright headers, paper references (the exception is about attribution, not infrastructure). Every hit that is NOT pure attribution is a finding — copy each hit line into the report with the file, line, and your one-line classification of what the reference appears to be (but do not edit anything).

### Item 3 — contributor path

Follow Contributor instructions alone: read `CONTRIBUTING.md` from the clone and do what it says a contributor does to set up and run the module tier, up to (but not including) anything requiring a cluster or external credentials. If the document succeeds in guiding you with no outside knowledge, the item passes; if you get stuck or need knowledge the repo does not carry, record exactly where you stalled and what was missing. Do not run any cluster smoke tier. Document which of the repository's claimed entry points you actually executed.

### Item 4 — expert readability

Reading only `README.md`, `docs/`, and `component-templates/` from the clone, answer in your own words (answers quoted in the report, no links pasted from memory): (a) what is CBSE; (b) how is it installed today (and what is stated about the install path's future); (c) what must a custom EDS/Translator/PostProcessingService fulfil to work with it; (d) what must a cluster admin enable/provide on the target cluster. If any of the four cannot be answered from those documents alone, the item records the gap.

### Item 5 — surface integrity

(a) Delete-nothing link check — resolve every relative markdown link in `README.md`, `CONTRIBUTING.md`, `CHANGELOG.md`, `SECURITY.md`, `docs/*.md`, `test/README.md`, `test/e2e/README.md`, `experiment-operator/README.md`, `scenario-manager/README.md`, `component-templates/translator/README.md`, `component-templates/scenario-detail-database/README.md`:

```bash
for f in <the files above>; do
  dir=$(dirname "$f")
  grep -oE '\]\([^)]+\)' "$f" | sed -E 's/^\]\(//; s/\)$//' \
  | while IFS= read -r link; do
      case "$link" in http*://*|mailto:*|\#*) continue;; esac
      target="${link%%#*}"; [ -n "$target" ] || continue
      [ -e "$dir/$target" ] || [ -e "$target" ] || printf 'BROKEN: %s -> %s\n' "$f" "$link"
    done
done
```

(b) Verify `CHANGELOG.md` exists and carries a seeded history structure (Keep-a-Changelog markers; `[Unreleased]`; an anchor for the `v0.1-jos-paper` tag if history mentions it).

(c) The intended checklist also asks whether "every settled slice's handoff names its evidence." The public repository may or may not contain development-log/handoff material. Record what you actually find: if handoff documents are absent, state that plainly as the item's finding; do not search the wider filesystem for them and never look outside the clone.

## Constraints

- Reviewer discipline: fixed nothing, edit only your `M0-REVIEW.md`, no `git commit` anywhere, no `git push`, no `kubectl`, no `KUBECONFIG`, no docker/registry operations, no contact with any cluster (the smoke tier is out of scope by definition; envtest inside `make test-fast` is a local throwaway API server and is fine).
- Secrets: if you encounter anything resembling a credential, token, or key material in the clone, do NOT reproduce its value — record the file and line and redact.
- Time budget: the cold test-fast run may take 10-30 minutes. That is expected; wait for it, capture its output to a file, and report its rc verbatim.

## Observable acceptance

Your `worker_done` must carry: a three-sentence executive summary (overall verdict language: which checklist items passed, which carried findings); both lifecycle IDs (your task id and dispatch id); an explicit `--outcome succeeded|failed` (succeeded = the review executed completely and honestly, regardless of how many findings it records — findings do not make the review fail; only your inability to execute an item does); the runtime attestation line (first checkpoint: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"`; on the target `ai.forge/qwen3.8-27b-nvfp4` — mismatch → stop, `--outcome failed` with the observed line); and your verdict table (item 1-5 → PASS / PASS-with-findings / BLOCKED + one line each). The full evidence lives in the clone's `M0-REVIEW.md` — quote its path in the report.
