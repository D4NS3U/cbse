# Implementation handoff

This file is the durable routing record for sequential implementation of [`FEATURE.md`](FEATURE.md). It is not proof that code or tests are correct; every agent verifies relevant evidence before relying on it. Keep this file free of credentials, credential-file paths, Secret contents, tokens, decoded authentication material, and test artifacts.

## Current checkpoint

- Base commit: `unset`
- Target: `S01-M1`
- State: `not-started`
- Resume at: `S01-M1 — Alpha4 API and CRD`
- Worktree summary: `unset`
- Blocker: `none`

Allowed states are:

- `not-started`: no implementation work is recorded for the target ID.
- `in-progress`: implementation has started and the first remaining ID is recorded.
- `implemented`: the owned behavior and tests are present, but required repository-root verification has not passed.
- `fast-verified`: focused tests and `make test-fast` passed for the recorded revision or unchanged worktree, but mandatory smoke has not passed.
- `smoke-verified`: the complete local gate, incoming deferred groups, `make test-fast`, and mandatory smoke passed for the recorded revision or unchanged worktree.
- `blocked`: progress or mandatory verification requires an unavailable external prerequisite or a reported specification decision.

## Completed groups

| Group | State | Evidence-bearing revision or worktree | Verification |
| --- | --- | --- | --- |
| None | `not-started` | `unset` | `not run` |

## Current-slice checklist

- [ ] Read `FEATURE.md`, the target slice, and every deferred group assigned to the target.
- [ ] Inspect relevant existing changes and record the base commit.
- [ ] Implement the target milestone without unrelated changes.
- [ ] Add or update the owned tests and generated artifacts.
- [ ] Run focused tests.
- [ ] Run `make test-fast`.
- [ ] Run `make test-smoke` when required.
- [ ] Record the first incomplete stable group or milestone below.

## Remaining work

- First incomplete group or milestone: `S01-M1`
- First concrete task: `Audit the current alpha2/alpha3 API and generated-CRD structure against Slice 01.`

## Verification log

| Date | Revision or worktree | Command | Result | Notes |
| --- | --- | --- | --- | --- |
| — | — | — | `not run` | — |

## Decisions and repository observations

- None recorded.

## Run history

Append one sanitized entry per agent run. Record the slice and milestone, what changed, tests and results, remaining stable ID, blocker category, and next action. Do not paste raw logs or sensitive paths.
