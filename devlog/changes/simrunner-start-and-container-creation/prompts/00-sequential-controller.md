# Sequential implementation controller prompt

You are implementing the seven ordered slices of the CBSE feature in this repository. Work autonomously, but keep the work bounded, reviewable, and resumable by a later coding-agent run.

## Read order and authority

Before exploring or changing implementation code:

1. Read [`../FEATURE.md`](../FEATURE.md) completely. It is the feature entry point and owns all cross-cutting contracts.
2. Read the repository-root [`AGENTS.md`](../../../../AGENTS.md) completely and obey its current test and cluster-safety contract, along with any more specific instructions that apply to files you touch.
3. Read the [slice dependency map](../FEATURE.md#slice-dependency-map), then read slice documents in numeric order until you can identify the earliest slice that is not complete. Read that target slice completely before changing code. If taking a second slice, read it completely only after the first has passed its completion gate.

Treat `FEATURE.md` and the seven files under `../slices/` as normative and read-only. Do not edit the specification to fit an implementation. If two requirements appear contradictory and repository inspection cannot resolve them, stop and report the exact conflicting sections.

## Objective and run limit

Implement the earliest incomplete slice. One completed slice is the default outcome for this run. You may implement the immediately following slice only when all of these conditions hold:

- the first slice satisfies every requirement and acceptance criterion in its slice document;
- all tests required for that slice, including smoke when required, have passed;
- the next slice is the next numeric slice and all of its prerequisites are complete;
- there is enough capacity to implement and verify the next slice without rushing, weakening tests, or leaving an avoidable partial integration.

Never skip a slice, work on non-consecutive slices, or start a third slice in the same run. A verification-blocked slice is not complete and blocks later slices.

## Establish the resume point

Inspect the current repository instead of assuming it is clean or that an earlier handoff is correct:

- Run `git status --short` and inspect relevant existing diffs before editing. Treat pre-existing changes as user work; preserve them and do not reset, overwrite, or reformat unrelated files.
- Compare implementation, generated artifacts, tests, and documentation against each slice's `Required behavior`, `Acceptance tests`, and `Completion and handoff` sections.
- Use code and test evidence to classify each inspected slice as:
  - `complete`: every owned requirement is implemented, required generated output is current, and every required test tier has credible passing evidence for the current worktree;
  - `incomplete`: one or more implementation, generated-output, documentation, or test requirements remain;
  - `verification-blocked`: implementation appears complete, but a required test cannot be run because an external prerequisite is unavailable.
- The first slice that is not `complete` is the only valid starting point. Continue partial work on it rather than recreating it.
- Do not use the presence of files, TODO comments, a status note, or an earlier agent's assertion as sole proof of completion.

## Implementation rules

- Implement the complete target slice, including owned tests, generated artifacts, and documentation. Requirements are not optional merely because they are large.
- Follow nearby repository patterns and the feature's ownership boundaries. Prefer small, explicit, idempotent behavior over broad refactors or speculative abstractions.
- Do not add compatibility behavior, out-of-scope features, insecure registry handling, TLS bypasses, or test-only shortcuts prohibited by the specification.
- Do not weaken, delete, skip, or rewrite tests to conceal a failure. Add focused tests for the slice's acceptance criteria and diagnose failures at their source.
- Do not expose credentials, credential-file paths, Secret payloads, tokens, or decoded authentication material in commands, logs, patches, reports, or test artifacts.
- Do not deploy directly into `default` or `kube-system`, delete the shared CRD, or delete the `cbse-test-system` namespace.
- Do not create commits. Leave the worktree ready for user review.

If you must stop mid-slice, preserve useful work and leave the repository buildable when reasonably possible. Mark the slice `incomplete`, identify the first unsatisfied acceptance criterion, and do not begin the next slice.

## Verification gate

During development, run focused tests that localize failures. Before calling an implementation slice complete:

1. Run the repository-root `make test-fast` exactly as required by `AGENTS.md`.
2. Run `make test-smoke` whenever the target slice or `AGENTS.md` requires it. Set `KUBECONFIG` explicitly, use only the approved cluster and immutable-image workflow, and supply the protected registry-auth input externally.
3. Inspect `git status --short` after testing. Do not add `artifacts/test/` or generated diagnostics to commits or handoff material.

Never invent a credential path, substitute a different cluster, weaken preflight, or bypass TLS to make smoke run. If a mandatory smoke prerequisite is unavailable, run all safe fast and targeted checks, classify the slice as `verification-blocked`, and stop without starting a later slice. State the missing category of input and show the exact smoke command with a redacted placeholder; do not print secret values or the protected credential's actual path.

## Required final handoff

End the run with a concise report using these headings:

1. `Slice status` — list every slice inspected and classify it as `complete`, `incomplete`, or `verification-blocked`; name the slice worked on.
2. `Implemented` — summarize behavior completed in this run, including generated artifacts and documentation.
3. `Files changed` — list the relevant paths and distinguish pre-existing changes when applicable.
4. `Tests` — list each command run and its pass, fail, or blocked result. Sanitize sensitive inputs.
5. `Remaining issues` — give concrete failures, blockers, or unverified acceptance criteria; write `none` only when accurate.
6. `Worktree` — summarize relevant uncommitted state and confirm that no commit was created.
7. `Resume at` — name exactly one slice number and the first remaining task, or state that all seven slices are complete.

Do not claim success based only on code written. A slice is complete only after its full completion and verification gate is satisfied.
