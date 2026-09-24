# Dispatch prompt — L2: Append the canonical Apache-2.0 APPENDIX section to LICENSE

**Task title:** L2 — LICENSE appendix completion (user decision 2026-09-24: append the missing canonical APPENDIX block to LICENSE; user also enabled GitHub private vulnerability reporting, unrelated to your file)

**Read order (mandatory, all normative and read-only for you):** `devlog/changes/public-repo-hygiene/FEATURE.md` §5; repository-root `AGENTS.md` (current text — normative for your run); then `LICENSE`. On any contradiction: stop and ask via your ask channel.

## Target (your ownership)

Exactly one file: `LICENSE`. Untouchable: everything else — including all source files that already carry canonical headers (L1, settled), `README.md`/`SECURITY.md` (S2, settled), and every spec/charter document.

## Change (exact, minimal, additive)

Pre-verified ground truth: `LICENSE` has 190 lines; `END OF TERMS AND CONDITIONS` is line 176; the applied notice (`Copyright 2026 Daniel Seufferth` + short-form license block) occupies the tail and must be **preserved byte-identical** (P3 — attribution stays). The canonical `APPENDIX` section heading and its instructions paragraph are missing.

Insert **exactly this block** between the `END OF TERMS AND CONDITIONS` line and the `Copyright 2026 Daniel Seufferth` line, matching the file's existing 3-space body indentation (keep the file's existing blank-line separation before and after):

```
   APPENDIX: How to apply the Apache License to your work.

      To apply the Apache License to your work, attach the following
      boilerplate notice, with the fields enclosed by brackets "[]"
      replaced with your own identifying information. (Don't include
      the brackets!)  The text should be enclosed in the appropriate
      comment syntax for the file format. Please do not remove any
      of the fields of the template.
```

Rationale you may rely on: the existing applied notice that follows the insertion is the appendix's filled-in instance — the LICENSE then carries the canonical appendix section while the real attribution stays untouched. Nothing else changes: no reflow, no re-indentation of surrounding lines, no notice edits.

## Constraints

- Documentation-only change: the tier contract requires no test tier; no cluster operations; no commits (P6).
- P3 verbatim: institution/holder attribution must survive; you do not edit the applied copyright lines.
- Scope discipline: exactly this one file; discovered gaps → report lines.

## Observable acceptance (all echoed in your worker_done; the manager re-runs each)

1. Attestation first: `printf '%s/%s\n' "$PI_PROVIDER" "$PI_MODEL"` → must print `ai.forge/qwen3.8-27b-nvfp4` (verbatim repeat in your executive summary; mismatch → stop, change nothing, `--outcome failed`).
2. Structure proof: `grep -n "APPENDIX\|END OF TERMS\|Copyright 2026" LICENSE` shows APPENDIX present exactly once, positioned after END OF TERMS and before Copyright 2026.
3. Diff discipline: `git diff LICENSE` shows exactly one additive hunk — +8 lines (`+9` `+10` if you preserved an extra spacing blank — one blank line before and after the block is the expectation), zero deletions, byte-for-byte the block text above.
4. Attribution preservation: the tail applied-notice block is unchanged (`git diff LICENSE | grep -c "^-"` → 0).
5. Line count: `wc -l LICENSE` → 198 (+8 from 190) or 199–200 if you added spacing blanks; echo it.
6. Containment: `git status --short` shows your edit as exactly ` M LICENSE` (everything else in the tree is the settled-uncommitted Package-A/L1/S2 change set — never yours to touch or revert).
