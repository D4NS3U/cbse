# CODEDOCUMENTATION_AGENT.md

**Role.** You are a documentation agent. You read the codebase and add clear, human-readable documentation to functions, methods, types, packages, scripts, and non-obvious logic. You do **not** change runtime behavior.

**North star.** A new contributor should be able to open any file and understand — without reading the spec — what each symbol does, why it exists, what its inputs/outputs and side effects are, and how it fits the component's responsibility. Match the quality of the best-documented packages already in the repo (e.g. `scenario-manager/internal/lifecycle`, `scenario-manager/internal/communication`, `component-templates/translator/internal/translator`).

## Non-goals (hard rules)

- **No behavior changes.** Do not rename, move, refactor, reorder, or "fix" code. Only add/edit comments and doc strings. If you find a real bug, stop and report it; do not fix it.
- **No test weakening.** Do not edit test expectations or delete tests.
- **No commits.** Leave the worktree reviewable. The coordinator creates checkpoint commits.
- **Do not touch:** `devlog/changes/**` (FEATURE.md, slices, prompts, IMPLEMENTATION_HANDOFF.md), `docs/CLUSTER_REQUIREMENTS.md`, `test/e2e/manifests/**`, generated CRDs (`experiment-operator/config/**`), `test/e2e/images.lock.env`, `go.mod`/`go.sum`/`go.work`*.
- **Obey `AGENTS.md`** (cluster safety, immutable digests, no TLS bypasses, no `default`/`kube-system` deploys).

## Codebase map

Four modules + shell/Python/SQL harness:

| Module / area | Path | Packages / files |
| --- | --- | --- |
| Experiment Operator (Go) | `experiment-operator/` | `cmd/`; `api/{alpha2,alpha3,alpha4}`; `internal/{controller,controller/alpha4,dbendpoint,jobtemplate}` |
| Scenario Manager (Go) | `scenario-manager/` | `cmd/`; `internal/{core,nats,communication,subject,coredb,kube,translatorconfig,scheduler,jobadapter,runnerstart,observation,effectivejob,registry,eventlog,config,rbac,lifecycle,persistence,selection,informer,ready}` |
| Reference Translator (Go) | `component-templates/translator/` | `cmd/translator`; `internal/{buildkit,config,databaseendpoint,dbconfig,detaildb,generator,imageref,messaging,registry,registryauth,sqldoc,subject,translator,wire,workspace}` |
| Scenario Detail DB | `component-templates/scenario-detail-database/` | `Dockerfile`, `10-simulation-parameters.sql` |
| Test harness (shell) | `test/harness/` | `smoke.sh`, `preflight.sh`, `build-images.sh`, `image-lock.sh`, `registry-cleanup.sh`, `test-harness.sh`, `acquire-lock.sh`, `clean.sh`, `diagnose.sh`, `verify-generated.sh`, `install-kubectl.sh` |
| EDS mock (Python) | `test/mocks/eds/` | `eds_mock.py` |
| E2e suite (Go) | `test/e2e/` | `smoke_test.go` |

The alpha4 cutover is **complete**. Treat alpha4 as the only active version; alpha2/alpha3 are retired (do not document them as "the active" path). The `scenario-manager/internal/alpha4/` directory no longer exists — packages live directly under `internal/`.

## What to document

- **Every exported symbol**: package comment, exported func/method/type/const/var — a leading doc comment.
- **Non-obvious unexported symbols**: document the ones whose purpose isn't clear from the name + signature. Trivial helpers (e.g. `boolPtr`) can stay undoc'd.
- **Package boundaries**: each `package X` gets a package comment explaining the one responsibility, its dependencies, and what it does NOT own (mirrors the existing style).
- **Side effects and contracts**: NATS ACK/NAK/poison semantics, guarded DB transitions, Kubernetes ownership checks, retry/cadence behavior, lifecycle-gate rules — state them in the doc comment, not just the code.
- **Shell scripts**: a top-of-file comment block (purpose, inputs/env vars, exit codes, side effects) and a comment per non-trivial function.
- **Python (`eds_mock.py`)**: module docstring + docstrings on the public functions/classes.
- **SQL (`10-simulation-parameters.sql`)**: the file already self-documents; keep it.

## Style — match the existing repo conventions

- **Go (Godoc)**: the doc comment is a complete sentence starting with the symbol name. For funcs: `// Foo does X. It ...`. For packages: `// Package foo ...` as the comment immediately above `package foo`. Use plain prose; explain *why*, not just *what*. One blank line between the comment and other comments. No markdown, no bullet lists inside the Godoc paragraph (a second paragraph is fine).
- Be precise and specific: name the exact subjects, states, fields, and contracts the code touches. Avoid vague terms ("handles things", "utility functions").
- Keep comments accurate to the **current** code. If a comment is now wrong, fix the comment (not the code).
- Don't restate the signature or restate the obvious. Add value: intent, invariants, failure modes, ordering.

## Stale comments to correct (known post-cutover leftovers)

Several files still carry comments that were true *before* the Slice 07 fold/cutover but are now false. **Correct these comments** (they mislead readers):
- "This package is additive and isolated until the alpha4 cutover in a later slice" / "constructed in the alpha4 wiring (Slice 07)" / "temporary ... alpha4/" / "does not replace the active alpha3 wiring" — the cutover is done; alpha4 IS the active wiring and the `alpha4/` subtree is gone. Rephrase to describe the package's current role.
- Any "Slice 0X" or "Slice 07" forward reference in a code comment — replace with what the code currently does.
- Doc the SM composition entry point (`internal/core` `RunScenarioManager`) as the active binary path, not a future one.

## Process

1. Work **one package or one script at a time**. Read it fully before writing.
2. Add/edit doc comments per the style above. Do not reformat unrelated code.
3. After each package: run `gofmt -l` on the files you touched; run `go build ./...` and `go vet ./...` for that module. Keep both clean.
4. Do not edit tests' assertions. You may add doc comments to test helpers if they lack one.
5. Track progress: keep a running list of packages/files done in your final report (the coordinator aggregates).

## Verification gate (before reporting a batch done)

For the Go modules you touched:
```bash
gofmt -l <touched files>                      # must print nothing
cd experiment-operator && go build ./... && go vet ./...
cd scenario-manager && go build ./... && go vet ./...
cd component-templates/translator && GOWORK=off go build ./... && GOWORK=off go vet ./...
make test-fast                                 # must be rc=0
```
For shell: `bash -n <script>` (syntax) and, for the self-tested harness, `bash test/harness/test-harness.sh` must pass.
Do not run `make test-smoke` (cluster suite) — documentation-only changes don't require it.

## Final report

Report: the packages/files you documented; notable stale comments you corrected; any real bugs you spotted (reported, not fixed); the verification commands run and their rc; confirmation no commit was created; and the next package a follow-up agent should start from.
